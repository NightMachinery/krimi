package krimi

import (
	"encoding/json"
	"log"

	"github.com/gorilla/websocket"
)

func (s *Store) registerSubscriber(gameID string, conn *websocket.Conn) *subscriber {
	sub := &subscriber{conn: conn}
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	if s.clients[gameID] == nil {
		s.clients[gameID] = map[*subscriber]struct{}{}
	}
	s.clients[gameID][sub] = struct{}{}
	return sub
}

func (s *Store) unregisterSubscriber(gameID string, sub *subscriber) {
	s.hubMu.Lock()
	defer s.hubMu.Unlock()
	s.unregisterSubscriberLocked(gameID, sub)
}

func (s *Store) unregisterSubscriberLocked(gameID string, sub *subscriber) {
	if subs, ok := s.clients[gameID]; ok {
		delete(subs, sub)
		if len(subs) == 0 {
			delete(s.clients, gameID)
		}
	}
}

func (s *Store) closeSubscribers(gameID string) {
	s.hubMu.Lock()
	subs := s.clients[gameID]
	delete(s.clients, gameID)
	s.hubMu.Unlock()
	for sub := range subs {
		_ = sub.close()
	}
}

func (s *Store) closeAllSubscribers() {
	s.hubMu.Lock()
	clients := s.clients
	s.clients = map[string]map[*subscriber]struct{}{}
	s.hubMu.Unlock()
	for _, subs := range clients {
		for sub := range subs {
			_ = sub.close()
		}
	}
}

func (s *Store) broadcastGame(game *Game) {
	message := snapshotMessage{Type: "snapshot", Game: game}
	blob, err := json.Marshal(message)
	if err != nil {
		log.Printf("broadcast marshal error: %v", err)
		return
	}
	s.hubMu.Lock()
	subs := make([]*subscriber, 0, len(s.clients[game.GameID]))
	for sub := range s.clients[game.GameID] {
		subs = append(subs, sub)
	}
	s.hubMu.Unlock()
	for _, sub := range subs {
		if err := sub.write(websocket.TextMessage, blob); err != nil {
			log.Printf("broadcast write error: %v", err)
			s.unregisterSubscriber(game.GameID, sub)
			_ = sub.close()
		}
	}
}

func (s *subscriber) write(messageType int, data []byte) error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn.WriteMessage(messageType, data)
}

func (s *subscriber) close() error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	return s.conn.Close()
}
