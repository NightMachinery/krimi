function buildHeaders(headers = {}) {
  return {
    Accept: "application/json",
    ...headers
  };
}

async function request(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: buildHeaders(options.headers)
  });

  const contentType = response.headers.get("content-type") || "";
  const rawBody = await response.text();
  const isJson = contentType.includes("application/json");
  const payload = isJson && rawBody ? JSON.parse(rawBody) : null;

  if (!response.ok) {
    const message = payload?.error || response.statusText || "Request failed";
    const error = new Error(message);
    error.status = response.status;
    throw error;
  }

  if (!isJson) {
    throw new Error(`Expected JSON response from ${path}`);
  }

  return payload;
}

function jsonRequest(path, method, body) {
  return request(path, {
    method,
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(body)
  });
}

export function createGame(lang) {
  return jsonRequest("/api/games", "POST", { lang });
}

export function getGame(gameId) {
  return request(`/api/games/${encodeURIComponent(gameId)}`);
}

export function addPlayer(gameId, nickname, slug) {
  return jsonRequest(
    `/api/games/${encodeURIComponent(gameId)}/players`,
    "POST",
    {
      nickname,
      slug
    }
  );
}

export function getPlayer(gameId, slug) {
  return request(
    `/api/games/${encodeURIComponent(gameId)}/players/${encodeURIComponent(
      slug
    )}`
  );
}

export function setDetective(gameId, detectiveIndex) {
  return jsonRequest(
    `/api/games/${encodeURIComponent(gameId)}/detective`,
    "POST",
    { detectiveIndex }
  );
}

export function startGame(gameId, detectiveIndex) {
  return jsonRequest(`/api/games/${encodeURIComponent(gameId)}/start`, "POST", {
    detectiveIndex
  });
}

export function setAnalysis(gameId, analysis) {
  return jsonRequest(
    `/api/games/${encodeURIComponent(gameId)}/analysis`,
    "POST",
    { analysis }
  );
}

export function setMurdererChoice(gameId, choice) {
  return jsonRequest(
    `/api/games/${encodeURIComponent(gameId)}/murderer-choice`,
    "POST",
    { choice }
  );
}

export function passTurn(gameId, playerId) {
  return jsonRequest(
    `/api/games/${encodeURIComponent(gameId)}/pass-turn`,
    "POST",
    {
      playerId
    }
  );
}

export function makeGuess(gameId, playerId, guess) {
  return jsonRequest(`/api/games/${encodeURIComponent(gameId)}/guess`, "POST", {
    playerId,
    guess
  });
}

export function buildWebSocketUrl(path) {
  const url = new URL(path, window.location.origin);
  url.protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return url.toString();
}

export function openGameSocket(gameId) {
  const url = buildWebSocketUrl(`/ws/games/${encodeURIComponent(gameId)}`);
  return new WebSocket(url);
}
