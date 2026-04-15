import router from "@/router";
import {
  addPlayer as apiAddPlayer,
  createGame as apiCreateGame,
  getGame,
  getPlayer,
  makeGuess as apiMakeGuess,
  openGameSocket,
  passTurn as apiPassTurn,
  setAnalysis as apiSetAnalysis,
  setDetective as apiSetDetective,
  setMurdererChoice as apiSetMurdererChoice,
  startGame as apiStartGame
} from "@/api/client";

let activeSocket = null;
let activeGameId = null;
let reconnectTimer = null;
let reconnectGeneration = 0;

function clearReconnectTimer() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

function disconnectSocket() {
  clearReconnectTimer();
  reconnectGeneration += 1;
  if (activeSocket) {
    activeSocket.onopen = null;
    activeSocket.onmessage = null;
    activeSocket.onerror = null;
    activeSocket.onclose = null;
    activeSocket.close();
  }
  activeSocket = null;
  activeGameId = null;
}

function connectSocket(context, gameId) {
  if (
    activeSocket &&
    activeGameId === gameId &&
    activeSocket.readyState !== WebSocket.CLOSED &&
    activeSocket.readyState !== WebSocket.CLOSING
  ) {
    return;
  }

  disconnectSocket();

  const generation = reconnectGeneration;
  activeGameId = gameId;
  activeSocket = openGameSocket(gameId);

  activeSocket.onmessage = event => {
    try {
      const payload = JSON.parse(event.data);
      if (payload?.type === "snapshot" && payload.game) {
        context.commit("setGame", payload.game);
      }
    } catch (error) {
      void error;
    }
  };

  activeSocket.onclose = () => {
    if (generation !== reconnectGeneration || activeGameId !== gameId) {
      return;
    }
    activeSocket = null;
    clearReconnectTimer();
    reconnectTimer = setTimeout(async () => {
      if (activeGameId !== gameId) return;
      try {
        await context.dispatch("loadGame", gameId);
      } catch (error) {
        void error;
      }
    }, 1000);
  };

  activeSocket.onerror = error => {
    void error;
  };
}

export default {
  async createGame(context, lang) {
    return apiCreateGame(lang);
  },

  async loadGame(context, gameId) {
    const game = await getGame(gameId);
    context.commit("setGame", game);
    connectSocket(context, gameId);
    return game;
  },

  disconnectGame() {
    disconnectSocket();
  },

  async startGame(context, payload) {
    const game = await apiStartGame(payload.gameId, payload.detectiveIndex);
    context.commit("setGame", game);
    return game;
  },

  async setDetective(context, payload) {
    const game = await apiSetDetective(payload.gameId, payload.player);
    context.commit("setGame", game);
    return game;
  },

  async setAnalysis(context, payload) {
    const game = await apiSetAnalysis(payload.gameId, payload.analysis);
    context.commit("setGame", game);
    return game;
  },

  async setMurdererChoice(context, payload) {
    const game = await apiSetMurdererChoice(payload.gameId, payload.choice);
    context.commit("setGame", game);
    return game;
  },

  async passTurn(context, payload) {
    const game = await apiPassTurn(payload.gameId, payload.player.playerId);
    context.commit("setGame", game);
    return game;
  },

  async makeGuess(context, payload) {
    const game = await apiMakeGuess(
      payload.gameId,
      payload.player.playerId,
      payload.guess
    );
    context.commit("setGame", game);
    return game;
  },

  async addPlayer(context, payload) {
    try {
      const response = await apiAddPlayer(
        payload.gameId,
        payload.nickname,
        payload.slug
      );
      context.commit("setPlayer", response.player);
      router.push(`/game/${payload.gameId}/player/${payload.slug}`);
      return false;
    } catch (error) {
      return error.message;
    }
  },

  async loadPlayer(context, payload) {
    const player = await getPlayer(payload.game, payload.player);
    context.commit("setPlayer", player);
    await context.dispatch("loadGame", payload.game);
    return player;
  }
};
