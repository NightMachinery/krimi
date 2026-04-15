package krimi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Server struct {
	store    *Store
	upgrader websocket.Upgrader
}

func NewServer(store *Store) *Server {
	return &Server{
		store: store,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/games", s.handleGamesRoot)
	mux.HandleFunc("/api/games/", s.handleGames)
	mux.HandleFunc("/ws/games/", s.handleGameWebSocket)
	return s.withLogging(s.withJSONHeaders(mux))
}

func (s *Server) withJSONHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(started).Round(time.Millisecond))
	})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleGamesRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/games" {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req createGameRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		game, err := s.store.CreateGame(r.Context(), req.Lang)
		if err != nil {
			s.handleStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, createGameResponse{GameID: game.GameID})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGames(w http.ResponseWriter, r *http.Request) {
	segments := splitPath(r.URL.Path)
	if len(segments) < 3 || segments[0] != "api" || segments[1] != "games" {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	gameID := segments[2]
	if len(segments) == 3 {
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		game, err := s.store.GetGame(r.Context(), gameID)
		if err != nil {
			s.handleStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, game)
		return
	}

	action := segments[3]
	switch action {
	case "players":
		s.handlePlayers(w, r, gameID, segments[4:])
	case "detective":
		s.handleDetective(w, r, gameID)
	case "start":
		s.handleStart(w, r, gameID)
	case "analysis":
		s.handleAnalysis(w, r, gameID)
	case "murderer-choice":
		s.handleMurdererChoice(w, r, gameID)
	case "pass-turn":
		s.handlePassTurn(w, r, gameID)
	case "guess":
		s.handleGuess(w, r, gameID)
	default:
		s.writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handlePlayers(w http.ResponseWriter, r *http.Request, gameID string, tail []string) {
	switch {
	case len(tail) == 0 && r.Method == http.MethodPost:
		var req addPlayerRequest
		if err := decodeJSON(r, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		player, _, err := s.store.AddPlayer(r.Context(), gameID, req.Nickname, req.Slug)
		if err != nil {
			s.handleStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusCreated, addPlayerResponse{Player: player})
	case len(tail) == 1 && r.Method == http.MethodGet:
		player, err := s.store.GetPlayer(r.Context(), gameID, tail[0])
		if err != nil {
			s.handleStoreError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, player)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleDetective(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req detectiveRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.SetDetective(r.Context(), gameID, req.DetectiveIndex)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req detectiveRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.StartGame(r.Context(), gameID, req.DetectiveIndex)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req analysisRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.SetAnalysis(r.Context(), gameID, req.Analysis)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handleMurdererChoice(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req murdererChoiceRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.SetMurdererChoice(r.Context(), gameID, req.Choice)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handlePassTurn(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req passTurnRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.PassTurn(r.Context(), gameID, req.PlayerID)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handleGuess(w http.ResponseWriter, r *http.Request, gameID string) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req guessRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	game, err := s.store.MakeGuess(r.Context(), gameID, req.PlayerID, req.Guess)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, game)
}

func (s *Server) handleGameWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	segments := splitPath(r.URL.Path)
	if len(segments) != 3 || segments[0] != "ws" || segments[1] != "games" {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	gameID := segments[2]
	game, err := s.store.GetGame(r.Context(), gameID)
	if err != nil {
		s.handleStoreError(w, err)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade error: %v", err)
		return
	}
	conn.SetReadLimit(1024)
	sub := s.store.registerSubscriber(gameID, conn)
	defer func() {
		s.store.unregisterSubscriber(gameID, sub)
		_ = sub.close()
	}()
	if err := writeSnapshot(sub, game); err != nil {
		log.Printf("websocket initial write error: %v", err)
		return
	}
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func writeSnapshot(sub *subscriber, game *Game) error {
	blob, err := json.Marshal(snapshotMessage{Type: "snapshot", Game: game})
	if err != nil {
		return err
	}
	return sub.write(websocket.TextMessage, blob)
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: message})
}

func (s *Server) handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		s.writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, ErrConflict):
		s.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrGameAlreadyStarted):
		s.writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("internal error: %v", err)
		s.writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func RunCleanupLoop(ctx context.Context, store *Store, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := store.CleanupExpiredRooms(context.Background()); err != nil {
				log.Printf("cleanup expired rooms: %v", err)
			}
		}
	}
}
