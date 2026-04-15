export function playersByIndex(game) {
  if (!game || !game.players) return [];
  return Object.keys(game.players)
    .map(playerId => game.players[playerId])
    .sort((a, b) => a.index - b.index);
}

export function findPlayerByIndex(game, playerIndex) {
  return playersByIndex(game).find(player => player.index === playerIndex);
}
