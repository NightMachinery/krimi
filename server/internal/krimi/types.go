package krimi

import "time"

type AnalysisItem struct {
	Title   string   `json:"title"`
	Type    int      `json:"type"`
	Options []string `json:"options"`
}

type MurdererChoice struct {
	Mean string `json:"mean"`
	Key  string `json:"key"`
}

type Guess struct {
	Player int    `json:"player"`
	Mean   string `json:"mean"`
	Key    string `json:"key"`
}

type Player struct {
	PlayerID  string `json:"playerId"`
	PlayerKey string `json:"playerkey"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Index     int    `json:"index"`
}

type Game struct {
	GameID           string             `json:"gameId"`
	Players          map[string]*Player `json:"players"`
	Detective        int                `json:"detective"`
	Started          bool               `json:"started,omitempty"`
	Finished         bool               `json:"finished"`
	Winner           string             `json:"winner,omitempty"`
	AvailableClues   int                `json:"availableClues"`
	Round            int                `json:"round"`
	Lang             string             `json:"lang"`
	Means            []string           `json:"means,omitempty"`
	Clues            []string           `json:"clues,omitempty"`
	Analysis         []AnalysisItem     `json:"analysis,omitempty"`
	Murderer         *int               `json:"murderer,omitempty"`
	ForensicAnalysis []string           `json:"forensicAnalysis,omitempty"`
	MurdererChoice   *MurdererChoice    `json:"murdererChoice,omitempty"`
	PassedTurns      []bool             `json:"passedTurns,omitempty"`
	Guesses          []*Guess           `json:"guesses,omitempty"`
	CreatedAt        time.Time          `json:"-"`
	UpdatedAt        time.Time          `json:"-"`
	ExpiresAt        time.Time          `json:"-"`
}

type snapshotMessage struct {
	Type string `json:"type"`
	Game *Game  `json:"game"`
}

type createGameRequest struct {
	Lang string `json:"lang"`
}

type createGameResponse struct {
	GameID string `json:"gameId"`
}

type addPlayerRequest struct {
	Nickname string `json:"nickname"`
	Slug     string `json:"slug"`
}

type addPlayerResponse struct {
	Player *Player `json:"player"`
}

type detectiveRequest struct {
	DetectiveIndex int `json:"detectiveIndex"`
}

type analysisRequest struct {
	Analysis []string `json:"analysis"`
}

type murdererChoiceRequest struct {
	Choice MurdererChoice `json:"choice"`
}

type passTurnRequest struct {
	PlayerID string `json:"playerId"`
}

type guessRequest struct {
	PlayerID string `json:"playerId"`
	Guess    Guess  `json:"guess"`
}

type errorResponse struct {
	Error string `json:"error"`
}
