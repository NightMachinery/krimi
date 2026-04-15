package krimi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrInvalid            = errors.New("invalid request")
	ErrGameAlreadyStarted = errors.New("game has already started")
)

const (
	defaultRoomCodeLength = 5
	roomCodeAlphabet      = "abcdefghijklmnopqrstuvwxyz0123456789"
)

type Store struct {
	db      *sql.DB
	ttl     time.Duration
	data    *gameDataCatalog
	mu      sync.Mutex
	hubMu   sync.Mutex
	clients map[string]map[*subscriber]struct{}
}

type subscriber struct {
	connMu sync.Mutex
	conn   websocketConn
}

type websocketConn interface {
	WriteMessage(messageType int, data []byte) error
	Close() error
}

func NewStore(dbPath string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	catalog, err := loadGameDataCatalog()
	if err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=1&_journal_mode=WAL", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{
		db:      db,
		ttl:     ttl,
		data:    catalog,
		clients: map[string]map[*subscriber]struct{}{},
	}
	if err := store.ensureSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.CleanupExpiredRooms(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	s.closeAllSubscribers()
	return s.db.Close()
}

func (s *Store) ensureSchema(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS games (
  game_id TEXT PRIMARY KEY,
  lang TEXT NOT NULL,
  detective INTEGER NOT NULL DEFAULT 0,
  started INTEGER NOT NULL DEFAULT 0,
  finished INTEGER NOT NULL DEFAULT 0,
  winner TEXT NOT NULL DEFAULT '',
  available_clues INTEGER NOT NULL DEFAULT 6,
  round INTEGER NOT NULL DEFAULT 1,
  means_json TEXT NOT NULL DEFAULT '[]',
  clues_json TEXT NOT NULL DEFAULT '[]',
  analysis_json TEXT NOT NULL DEFAULT '[]',
  murderer INTEGER NOT NULL DEFAULT -1,
  forensic_analysis_json TEXT NOT NULL DEFAULT '[]',
  murderer_choice_json TEXT NOT NULL DEFAULT 'null',
  passed_turns_json TEXT NOT NULL DEFAULT '[]',
  guesses_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS players (
  player_id TEXT PRIMARY KEY,
  game_id TEXT NOT NULL REFERENCES games(game_id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  player_index INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(game_id, slug),
  UNIQUE(game_id, player_index)
);
CREATE INDEX IF NOT EXISTS idx_players_game_id ON players(game_id);
CREATE INDEX IF NOT EXISTS idx_games_expires_at ON games(expires_at);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("ensure schema: %w", err)
	}
	return nil
}

func (s *Store) CleanupExpiredRooms(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupExpiredRoomsLocked(ctx)
}

func (s *Store) cleanupExpiredRoomsLocked(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT game_id FROM games WHERE expires_at <= ?`, nowUTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("select expired rooms: %w", err)
	}
	defer rows.Close()

	var expired []string
	for rows.Next() {
		var gameID string
		if err := rows.Scan(&gameID); err != nil {
			return fmt.Errorf("scan expired room: %w", err)
		}
		expired = append(expired, gameID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate expired rooms: %w", err)
	}
	if len(expired) == 0 {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM games WHERE expires_at <= ?`, nowUTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("delete expired rooms: %w", err)
	}
	for _, gameID := range expired {
		s.closeSubscribers(gameID)
	}
	return nil
}

func (s *Store) CreateGame(ctx context.Context, lang string) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cleanupExpiredRoomsLocked(ctx); err != nil {
		return nil, err
	}
	lang = normalizeLang(lang)
	now := nowUTC()
	game := &Game{
		Lang:           lang,
		Players:        map[string]*Player{},
		Detective:      0,
		Finished:       false,
		AvailableClues: 6,
		Round:          1,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(s.ttl),
	}
	for attempts := 0; attempts < 32; attempts++ {
		game.GameID = randomRoomCode(defaultRoomCodeLength)
		_, err := s.db.ExecContext(
			ctx,
			`INSERT INTO games (
			  game_id, lang, detective, started, finished, winner, available_clues, round,
			  means_json, clues_json, analysis_json, murderer, forensic_analysis_json,
			  murderer_choice_json, passed_turns_json, guesses_json,
			  created_at, updated_at, expires_at
			) VALUES (?, ?, ?, 0, 0, '', ?, ?, '[]', '[]', '[]', -1, '[]', 'null', '[]', '[]', ?, ?, ?)`,
			game.GameID,
			game.Lang,
			game.Detective,
			game.AvailableClues,
			game.Round,
			game.CreatedAt.Format(time.RFC3339Nano),
			game.UpdatedAt.Format(time.RFC3339Nano),
			game.ExpiresAt.Format(time.RFC3339Nano),
		)
		if err == nil {
			return game, nil
		}
		if !isUniqueConstraintError(err) {
			return nil, fmt.Errorf("insert game: %w", err)
		}
	}
	return nil, fmt.Errorf("%w: could not create unique room code", ErrConflict)
}

func (s *Store) GetGame(ctx context.Context, gameID string) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.touchGameLocked(ctx, gameID); err != nil {
		return nil, err
	}
	game, err := s.loadGameLocked(ctx, nil, gameID)
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) GetPlayer(ctx context.Context, gameID, slug string) (*Player, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.touchGameLocked(ctx, gameID); err != nil {
		return nil, err
	}
	player, err := s.loadPlayerLocked(ctx, nil, gameID, slug)
	if err != nil {
		return nil, err
	}
	return player, nil
}

func (s *Store) AddPlayer(ctx context.Context, gameID, nickname, slug string) (*Player, *Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollback(tx)

	game, err := s.loadGameLocked(ctx, tx, gameID)
	if err != nil {
		return nil, nil, err
	}
	existing, err := s.loadPlayerLocked(ctx, tx, gameID, slug)
	if err == nil {
		if err := s.touchGameTx(ctx, tx, gameID); err != nil {
			return nil, nil, err
		}
		game, err = s.loadGameLocked(ctx, tx, gameID)
		if err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit tx: %w", err)
		}
		return existing, game, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	if game.Started {
		return nil, nil, fmt.Errorf("%w: game has already started and no new players can join", ErrInvalid)
	}
	nickname = strings.TrimSpace(nickname)
	slug = strings.TrimSpace(strings.ToLower(slug))
	if nickname == "" || slug == "" {
		return nil, nil, fmt.Errorf("%w: nickname and slug are required", ErrInvalid)
	}
	count, err := s.playerCountLocked(ctx, tx, gameID)
	if err != nil {
		return nil, nil, err
	}
	playerID, err := randomID(8)
	if err != nil {
		return nil, nil, err
	}
	now := nowUTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO players (player_id, game_id, slug, name, player_index, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		playerID,
		gameID,
		slug,
		nickname,
		count,
		now,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, nil, fmt.Errorf("%w: player slug is already taken", ErrConflict)
		}
		return nil, nil, fmt.Errorf("insert player: %w", err)
	}
	if err := s.touchGameTx(ctx, tx, gameID); err != nil {
		return nil, nil, err
	}
	player, err := s.loadPlayerByIDLocked(ctx, tx, playerID)
	if err != nil {
		return nil, nil, err
	}
	game, err = s.loadGameLocked(ctx, tx, gameID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit tx: %w", err)
	}
	s.broadcastGame(game)
	return player, game, nil
}

func (s *Store) SetDetective(ctx context.Context, gameID string, detectiveIndex int) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		players := playersInOrder(game.Players)
		if detectiveIndex < 0 || detectiveIndex >= len(players) {
			return fmt.Errorf("%w: detective index out of range", ErrInvalid)
		}
		_, err := tx.ExecContext(ctx, `UPDATE games SET detective = ?, updated_at = ?, expires_at = ? WHERE game_id = ?`, detectiveIndex, nowUTC().Format(time.RFC3339Nano), nowUTC().Add(s.ttl).Format(time.RFC3339Nano), gameID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) StartGame(ctx context.Context, gameID string, detectiveIndex int) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		if game.Started {
			return ErrGameAlreadyStarted
		}
		players := playersInOrder(game.Players)
		if len(players) < 5 {
			return fmt.Errorf("%w: at least 5 players are required", ErrInvalid)
		}
		if detectiveIndex < 0 || detectiveIndex >= len(players) {
			return fmt.Errorf("%w: detective index out of range", ErrInvalid)
		}
		locale := s.data.Locale(game.Lang)
		means, err := randomSubset(locale.Means, len(players)*4)
		if err != nil {
			return err
		}
		clues, err := randomSubset(locale.Clues, len(players)*4)
		if err != nil {
			return err
		}
		analysis, err := buildAnalysis(locale.Analysis)
		if err != nil {
			return err
		}
		murdererIndex, err := chooseMurderer(players, detectiveIndex)
		if err != nil {
			return err
		}
		now := nowUTC()
		_, err = tx.ExecContext(
			ctx,
			`UPDATE games SET detective = ?, started = 1, available_clues = 6, round = 1, means_json = ?, clues_json = ?, analysis_json = ?, murderer = ?, forensic_analysis_json = '[]', murderer_choice_json = 'null', passed_turns_json = '[]', guesses_json = '[]', winner = '', finished = 0, updated_at = ?, expires_at = ? WHERE game_id = ?`,
			detectiveIndex,
			mustJSON(means),
			mustJSON(clues),
			mustJSON(analysis),
			murdererIndex,
			now.Format(time.RFC3339Nano),
			now.Add(s.ttl).Format(time.RFC3339Nano),
			gameID,
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) SetAnalysis(ctx context.Context, gameID string, analysis []string) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		if !game.Started {
			return fmt.Errorf("%w: game has not started", ErrInvalid)
		}
		if game.MurdererChoice == nil {
			return fmt.Errorf("%w: murderer choice is not set", ErrInvalid)
		}
		if len(analysis) != game.AvailableClues {
			return fmt.Errorf("%w: analysis must contain %d clues", ErrInvalid, game.AvailableClues)
		}
		for index, value := range analysis {
			if index >= len(game.Analysis) {
				return fmt.Errorf("%w: analysis index out of range", ErrInvalid)
			}
			if !containsString(game.Analysis[index].Options, value) {
				return fmt.Errorf("%w: invalid analysis option %q", ErrInvalid, value)
			}
		}
		now := nowUTC()
		_, err := tx.ExecContext(ctx, `UPDATE games SET forensic_analysis_json = ?, updated_at = ?, expires_at = ? WHERE game_id = ?`, mustJSON(analysis), now.Format(time.RFC3339Nano), now.Add(s.ttl).Format(time.RFC3339Nano), gameID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) SetMurdererChoice(ctx context.Context, gameID string, choice MurdererChoice) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		if !game.Started {
			return fmt.Errorf("%w: game has not started", ErrInvalid)
		}
		murderer := playersInOrder(game.Players)[*game.Murderer]
		means, clues, err := playerCards(game, murderer.Index)
		if err != nil {
			return err
		}
		if !containsString(means, choice.Mean) || !containsString(clues, choice.Key) {
			return fmt.Errorf("%w: murderer choice must come from the murderer's cards", ErrInvalid)
		}
		now := nowUTC()
		_, err = tx.ExecContext(ctx, `UPDATE games SET murderer_choice_json = ?, updated_at = ?, expires_at = ? WHERE game_id = ?`, mustJSON(choice), now.Format(time.RFC3339Nano), now.Add(s.ttl).Format(time.RFC3339Nano), gameID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) PassTurn(ctx context.Context, gameID, playerID string) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		player, err := s.loadPlayerByIDLocked(ctx, tx, playerID)
		if err != nil {
			return err
		}
		if player.Index == game.Detective {
			return fmt.Errorf("%w: the detective cannot pass a turn", ErrInvalid)
		}
		if err := ensureActiveGame(game); err != nil {
			return err
		}
		playerCount := len(game.Players)
		passed := ensureBoolSlice(game.PassedTurns, playerCount)
		passed[player.Index] = true
		game.PassedTurns = passed
		evaluateGameOutcome(game)
		return s.persistGameState(ctx, tx, game)
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) MakeGuess(ctx context.Context, gameID, playerID string, guess Guess) (*Game, error) {
	game, err := s.mutateGame(ctx, gameID, func(ctx context.Context, tx *sql.Tx, game *Game) error {
		player, err := s.loadPlayerByIDLocked(ctx, tx, playerID)
		if err != nil {
			return err
		}
		if player.Index == game.Detective {
			return fmt.Errorf("%w: the detective cannot make guesses", ErrInvalid)
		}
		if err := ensureActiveGame(game); err != nil {
			return err
		}
		guesserCards, err := guessTargetCards(game, guess.Player)
		if err != nil {
			return err
		}
		if !containsString(guesserCards.means, guess.Mean) || !containsString(guesserCards.clues, guess.Key) {
			return fmt.Errorf("%w: guesses must use the selected suspect's cards", ErrInvalid)
		}
		playerCount := len(game.Players)
		guesses := ensureGuessSlice(game.Guesses, playerCount)
		guesses[player.Index] = &Guess{Player: guess.Player, Mean: guess.Mean, Key: guess.Key}
		game.Guesses = guesses
		evaluateGameOutcome(game)
		return s.persistGameState(ctx, tx, game)
	})
	if err != nil {
		return nil, err
	}
	return game, nil
}

func (s *Store) mutateGame(ctx context.Context, gameID string, fn func(context.Context, *sql.Tx, *Game) error) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer rollback(tx)
	game, err := s.loadGameLocked(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	if err := fn(ctx, tx, game); err != nil {
		return nil, err
	}
	game, err = s.loadGameLocked(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	s.broadcastGame(game)
	return game, nil
}

func (s *Store) persistGameState(ctx context.Context, tx *sql.Tx, game *Game) error {
	now := nowUTC()
	_, err := tx.ExecContext(
		ctx,
		`UPDATE games SET finished = ?, winner = ?, available_clues = ?, round = ?, forensic_analysis_json = ?, murderer_choice_json = ?, passed_turns_json = ?, guesses_json = ?, updated_at = ?, expires_at = ? WHERE game_id = ?`,
		boolToInt(game.Finished),
		game.Winner,
		game.AvailableClues,
		game.Round,
		mustJSON(game.ForensicAnalysis),
		mustJSONNullable(game.MurdererChoice),
		mustJSON(game.PassedTurns),
		mustJSON(game.Guesses),
		now.Format(time.RFC3339Nano),
		now.Add(s.ttl).Format(time.RFC3339Nano),
		game.GameID,
	)
	if err != nil {
		return fmt.Errorf("update game state: %w", err)
	}
	return nil
}

func (s *Store) loadGameLocked(ctx context.Context, tx *sql.Tx, gameID string) (*Game, error) {
	row := queryRowContext(tx, s.db, ctx, `SELECT game_id, lang, detective, started, finished, winner, available_clues, round, means_json, clues_json, analysis_json, murderer, forensic_analysis_json, murderer_choice_json, passed_turns_json, guesses_json, created_at, updated_at, expires_at FROM games WHERE game_id = ?`, gameID)
	var (
		game               Game
		startedInt         int
		finishedInt        int
		meansJSON          string
		cluesJSON          string
		analysisJSON       string
		murderer           int
		forensicJSON       string
		murdererChoiceJSON string
		passedTurnsJSON    string
		guessesJSON        string
		createdAt          string
		updatedAt          string
		expiresAt          string
	)
	if err := row.Scan(
		&game.GameID,
		&game.Lang,
		&game.Detective,
		&startedInt,
		&finishedInt,
		&game.Winner,
		&game.AvailableClues,
		&game.Round,
		&meansJSON,
		&cluesJSON,
		&analysisJSON,
		&murderer,
		&forensicJSON,
		&murdererChoiceJSON,
		&passedTurnsJSON,
		&guessesJSON,
		&createdAt,
		&updatedAt,
		&expiresAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load game %s: %w", gameID, err)
	}
	game.Started = startedInt == 1
	game.Finished = finishedInt == 1
	game.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	game.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	game.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	if err := json.Unmarshal([]byte(meansJSON), &game.Means); err != nil {
		return nil, fmt.Errorf("decode means: %w", err)
	}
	if err := json.Unmarshal([]byte(cluesJSON), &game.Clues); err != nil {
		return nil, fmt.Errorf("decode clues: %w", err)
	}
	if err := json.Unmarshal([]byte(analysisJSON), &game.Analysis); err != nil {
		return nil, fmt.Errorf("decode analysis: %w", err)
	}
	if murderer >= 0 {
		m := murderer
		game.Murderer = &m
	}
	if murdererChoiceJSON != "null" && murdererChoiceJSON != "" {
		var choice MurdererChoice
		if err := json.Unmarshal([]byte(murdererChoiceJSON), &choice); err != nil {
			return nil, fmt.Errorf("decode murderer choice: %w", err)
		}
		game.MurdererChoice = &choice
	}
	if err := json.Unmarshal([]byte(forensicJSON), &game.ForensicAnalysis); err != nil {
		return nil, fmt.Errorf("decode forensic analysis: %w", err)
	}
	if err := json.Unmarshal([]byte(passedTurnsJSON), &game.PassedTurns); err != nil {
		return nil, fmt.Errorf("decode passed turns: %w", err)
	}
	if err := json.Unmarshal([]byte(guessesJSON), &game.Guesses); err != nil {
		return nil, fmt.Errorf("decode guesses: %w", err)
	}
	players, err := s.loadPlayersForGameLocked(ctx, tx, gameID)
	if err != nil {
		return nil, err
	}
	game.Players = players
	return &game, nil
}

func (s *Store) loadPlayersForGameLocked(ctx context.Context, tx *sql.Tx, gameID string) (map[string]*Player, error) {
	rows, err := queryContext(tx, s.db, ctx, `SELECT player_id, slug, name, player_index FROM players WHERE game_id = ? ORDER BY player_index ASC`, gameID)
	if err != nil {
		return nil, fmt.Errorf("select players: %w", err)
	}
	defer rows.Close()
	players := map[string]*Player{}
	for rows.Next() {
		player := &Player{}
		if err := rows.Scan(&player.PlayerID, &player.Slug, &player.Name, &player.Index); err != nil {
			return nil, fmt.Errorf("scan player: %w", err)
		}
		player.PlayerKey = player.PlayerID
		players[player.PlayerID] = player
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate players: %w", err)
	}
	return players, nil
}

func (s *Store) loadPlayerLocked(ctx context.Context, tx *sql.Tx, gameID, slug string) (*Player, error) {
	row := queryRowContext(tx, s.db, ctx, `SELECT player_id, slug, name, player_index FROM players WHERE game_id = ? AND slug = ?`, gameID, slug)
	player := &Player{}
	if err := row.Scan(&player.PlayerID, &player.Slug, &player.Name, &player.Index); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load player: %w", err)
	}
	player.PlayerKey = player.PlayerID
	return player, nil
}

func (s *Store) loadPlayerByIDLocked(ctx context.Context, tx *sql.Tx, playerID string) (*Player, error) {
	row := queryRowContext(tx, s.db, ctx, `SELECT player_id, slug, name, player_index FROM players WHERE player_id = ?`, playerID)
	player := &Player{}
	if err := row.Scan(&player.PlayerID, &player.Slug, &player.Name, &player.Index); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load player by id: %w", err)
	}
	player.PlayerKey = player.PlayerID
	return player, nil
}

func (s *Store) playerCountLocked(ctx context.Context, tx *sql.Tx, gameID string) (int, error) {
	row := queryRowContext(tx, s.db, ctx, `SELECT COUNT(*) FROM players WHERE game_id = ?`, gameID)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count players: %w", err)
	}
	return count, nil
}

func (s *Store) touchGameLocked(ctx context.Context, gameID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE games SET expires_at = ? WHERE game_id = ?`, nowUTC().Add(s.ttl).Format(time.RFC3339Nano), gameID)
	if err != nil {
		return fmt.Errorf("touch game: %w", err)
	}
	return nil
}

func (s *Store) touchGameTx(ctx context.Context, tx *sql.Tx, gameID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE games SET expires_at = ? WHERE game_id = ?`, nowUTC().Add(s.ttl).Format(time.RFC3339Nano), gameID)
	if err != nil {
		return fmt.Errorf("touch game: %w", err)
	}
	return nil
}

func queryRowContext(tx *sql.Tx, db *sql.DB, ctx context.Context, query string, args ...any) *sql.Row {
	if tx != nil {
		return tx.QueryRowContext(ctx, query, args...)
	}
	return db.QueryRowContext(ctx, query, args...)
}

func queryContext(tx *sql.Tx, db *sql.DB, ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if tx != nil {
		return tx.QueryContext(ctx, query, args...)
	}
	return db.QueryContext(ctx, query, args...)
}

func rollback(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}

func normalizeLang(lang string) string {
	lang = strings.TrimSpace(strings.ToLower(lang))
	if lang == "" {
		return "en"
	}
	if lang == "pt-br" {
		return "pt_br"
	}
	return lang
}

func nowUTC() time.Time {
	return time.Now().UTC()
}

func randomRoomCode(length int) string {
	builder := strings.Builder{}
	builder.Grow(length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(roomCodeAlphabet))))
		if err != nil {
			builder.WriteByte(roomCodeAlphabet[i%len(roomCodeAlphabet)])
			continue
		}
		builder.WriteByte(roomCodeAlphabet[n.Int64()])
	}
	return builder.String()
}

func randomID(byteLength int) (string, error) {
	bytes := make([]byte, byteLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func isUniqueConstraintError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique")
}

func mustJSON(value any) string {
	blob, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(blob)
}

func mustJSONNullable(value any) string {
	if value == nil {
		return "null"
	}
	return mustJSON(value)
}

func playersInOrder(players map[string]*Player) []*Player {
	ordered := make([]*Player, 0, len(players))
	for _, player := range players {
		ordered = append(ordered, player)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Index < ordered[j].Index
	})
	return ordered
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type playerCardSet struct {
	means []string
	clues []string
}

func playerCards(game *Game, playerIndex int) ([]string, []string, error) {
	if playerIndex < 0 {
		return nil, nil, fmt.Errorf("%w: invalid player index", ErrInvalid)
	}
	start := playerIndex * 4
	end := start + 4
	if end > len(game.Means) || end > len(game.Clues) {
		return nil, nil, fmt.Errorf("%w: player cards are out of range", ErrInvalid)
	}
	return append([]string(nil), game.Means[start:end]...), append([]string(nil), game.Clues[start:end]...), nil
}

func guessTargetCards(game *Game, playerIndex int) (playerCardSet, error) {
	if playerIndex == game.Detective {
		return playerCardSet{}, fmt.Errorf("%w: cannot guess the detective", ErrInvalid)
	}
	means, clues, err := playerCards(game, playerIndex)
	if err != nil {
		return playerCardSet{}, err
	}
	return playerCardSet{means: means, clues: clues}, nil
}

func ensureBoolSlice(values []bool, length int) []bool {
	if len(values) >= length {
		copied := append([]bool(nil), values...)
		return copied[:length]
	}
	result := make([]bool, length)
	copy(result, values)
	return result
}

func ensureGuessSlice(values []*Guess, length int) []*Guess {
	if len(values) >= length {
		copied := append([]*Guess(nil), values...)
		return copied[:length]
	}
	result := make([]*Guess, length)
	copy(result, values)
	return result
}

func ensureActiveGame(game *Game) error {
	if !game.Started {
		return fmt.Errorf("%w: game has not started", ErrInvalid)
	}
	if game.Finished {
		return fmt.Errorf("%w: game is already finished", ErrInvalid)
	}
	return nil
}

func chooseMurderer(players []*Player, detectiveIndex int) (int, error) {
	candidates := make([]int, 0, len(players)-1)
	for _, player := range players {
		if player.Index != detectiveIndex {
			candidates = append(candidates, player.Index)
		}
	}
	if len(candidates) == 0 {
		return 0, fmt.Errorf("%w: no murderer candidates available", ErrInvalid)
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return 0, fmt.Errorf("choose murderer: %w", err)
	}
	return candidates[n.Int64()], nil
}

func randomSubset[T any](items []T, count int) ([]T, error) {
	if count > len(items) {
		return nil, fmt.Errorf("%w: requested %d values from %d items", ErrInvalid, count, len(items))
	}
	indices := make([]int, len(items))
	for i := range items {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, fmt.Errorf("shuffle items: %w", err)
		}
		j := int(n.Int64())
		indices[i], indices[j] = indices[j], indices[i]
	}
	result := make([]T, count)
	for i := 0; i < count; i++ {
		result[i] = items[indices[i]]
	}
	return result, nil
}

func buildAnalysis(items []AnalysisItem) ([]AnalysisItem, error) {
	var cause []AnalysisItem
	var location []AnalysisItem
	var other []AnalysisItem
	for _, item := range items {
		switch item.Type {
		case 0:
			cause = append(cause, item)
		case 1:
			location = append(location, item)
		case 2:
			other = append(other, item)
		}
	}
	locationPick, err := randomSubset(location, 1)
	if err != nil {
		return nil, err
	}
	otherPick, err := randomSubset(other, 6)
	if err != nil {
		return nil, err
	}
	result := append([]AnalysisItem{}, cause...)
	result = append(result, locationPick...)
	result = append(result, otherPick...)
	return result, nil
}

func evaluateGameOutcome(game *Game) {
	playerCount := len(game.Players)
	nonDetectives := playerCount - 1
	validGuesses := 0
	actionsThisRound := 0
	correctGuess := false
	for _, guess := range game.Guesses {
		if guess != nil && guess.Key != "" {
			validGuesses++
			actionsThisRound++
			if game.MurdererChoice != nil && game.Murderer != nil && guess.Mean == game.MurdererChoice.Mean && guess.Key == game.MurdererChoice.Key {
				correctGuess = true
			}
		}
	}
	for _, passed := range game.PassedTurns {
		if passed {
			actionsThisRound++
		}
	}
	if correctGuess {
		game.Finished = true
		game.Winner = "detectives"
		return
	}
	if validGuesses == nonDetectives {
		game.Finished = true
		game.Winner = "murderer"
		return
	}
	if game.Round >= 3 && actionsThisRound == nonDetectives {
		game.Finished = true
		game.Winner = "murderer"
		return
	}
	if actionsThisRound == nonDetectives {
		game.Round++
		if game.AvailableClues < len(game.Analysis) {
			game.AvailableClues++
		}
		game.PassedTurns = make([]bool, playerCount)
	}
}
