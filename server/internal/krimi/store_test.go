package krimi

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateJoinReuseSlugAndCleanup(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	game, err := store.CreateGame(ctx, "en")
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	playerA, _, err := store.AddPlayer(ctx, game.GameID, "Alice", "alice")
	if err != nil {
		t.Fatalf("AddPlayer Alice: %v", err)
	}
	playerB, _, err := store.AddPlayer(ctx, game.GameID, "Alice Again", "alice")
	if err != nil {
		t.Fatalf("AddPlayer existing Alice: %v", err)
	}
	if playerA.PlayerID != playerB.PlayerID {
		t.Fatalf("expected existing slug to reuse original player, got %s vs %s", playerA.PlayerID, playerB.PlayerID)
	}

	_, err = store.db.ExecContext(ctx, `UPDATE games SET expires_at = ? WHERE game_id = ?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), game.GameID)
	if err != nil {
		t.Fatalf("expire game: %v", err)
	}
	if err := store.CleanupExpiredRooms(ctx); err != nil {
		t.Fatalf("CleanupExpiredRooms: %v", err)
	}
	if _, err := store.GetGame(ctx, game.GameID); err == nil {
		t.Fatalf("expected expired game to be deleted")
	}
}

func TestStartGamePassesAdvanceRound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	game, players := createStartedGame(t, store, ctx)

	guessingPlayer := firstEligibleGuesser(players, game)
	means, clues, err := playerCards(game, *game.Murderer)
	if err != nil {
		t.Fatalf("playerCards: %v", err)
	}
	if _, err := store.SetMurdererChoice(ctx, game.GameID, MurdererChoice{Mean: means[0], Key: clues[0]}); err != nil {
		t.Fatalf("SetMurdererChoice: %v", err)
	}

	if _, err := store.MakeGuess(ctx, game.GameID, guessingPlayer.PlayerID, Guess{Player: *game.Murderer, Mean: means[1], Key: clues[0]}); err != nil {
		t.Fatalf("MakeGuess: %v", err)
	}
	for _, player := range players {
		if player.Index == game.Detective || player.PlayerID == guessingPlayer.PlayerID {
			continue
		}
		if _, err := store.PassTurn(ctx, game.GameID, player.PlayerID); err != nil {
			t.Fatalf("PassTurn(%s): %v", player.PlayerID, err)
		}
	}

	updated, err := store.GetGame(ctx, game.GameID)
	if err != nil {
		t.Fatalf("GetGame: %v", err)
	}
	if updated.Round != 2 {
		t.Fatalf("expected round 2 after all non-detectives acted, got %d", updated.Round)
	}
	if updated.AvailableClues != 7 {
		t.Fatalf("expected 7 available clues after round advance, got %d", updated.AvailableClues)
	}
	if updated.Finished {
		t.Fatalf("expected game to continue after incorrect guesses and passes")
	}
}

func TestCorrectGuessEndsGameForDetectives(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	game, players := createStartedGame(t, store, ctx)

	means, clues, err := playerCards(game, *game.Murderer)
	if err != nil {
		t.Fatalf("playerCards: %v", err)
	}
	game, err = store.SetMurdererChoice(ctx, game.GameID, MurdererChoice{Mean: means[0], Key: clues[0]})
	if err != nil {
		t.Fatalf("SetMurdererChoice: %v", err)
	}

	analysis := make([]string, game.AvailableClues)
	for index := range analysis {
		analysis[index] = game.Analysis[index].Options[0]
	}
	if _, err := store.SetAnalysis(ctx, game.GameID, analysis); err != nil {
		t.Fatalf("SetAnalysis: %v", err)
	}

	guesser := firstEligibleGuesser(players, game)
	updated, err := store.MakeGuess(ctx, game.GameID, guesser.PlayerID, Guess{Player: *game.Murderer, Mean: means[0], Key: clues[0]})
	if err != nil {
		t.Fatalf("MakeGuess: %v", err)
	}
	if !updated.Finished {
		t.Fatalf("expected correct guess to finish the game")
	}
	if updated.Winner != "detectives" {
		t.Fatalf("expected detectives to win, got %q", updated.Winner)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "krimi.sqlite"), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func createStartedGame(t *testing.T, store *Store, ctx context.Context) (*Game, []*Player) {
	t.Helper()
	game, err := store.CreateGame(ctx, "en")
	if err != nil {
		t.Fatalf("CreateGame: %v", err)
	}

	var players []*Player
	for _, name := range []string{"Alice", "Bob", "Cara", "Dylan", "Erin"} {
		player, _, err := store.AddPlayer(ctx, game.GameID, name, strings.ToLower(name))
		if err != nil {
			t.Fatalf("AddPlayer(%s): %v", name, err)
		}
		players = append(players, player)
	}
	game, err = store.StartGame(ctx, game.GameID, 0)
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	return game, players
}

func firstEligibleGuesser(players []*Player, game *Game) *Player {
	for _, player := range players {
		if player.Index != game.Detective && player.Index != *game.Murderer {
			return player
		}
	}
	return nil
}
