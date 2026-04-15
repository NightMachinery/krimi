<template>
  <v-container style="height:100%">
    <v-row style="height:100%" align="center" v-if="game">
      <v-col class="mt-10 offset-xl-3 offset-lg-2" cols="12" md="5" xl="4">
        <h2 class="display-2">
          {{ t("Lobby for room") }}
          <code class="accent--text text-uppercase">{{
            $route.params.id
          }}</code>
        </h2>
        <p class="subtitle-1 my-4">
          {{ t("Waiting for players") }}. {{ playerCount }}
        </p>
        <v-progress-linear
          indeterminate
          absolute
          bottom
          rounded
          color="accent"
        ></v-progress-linear>
        <lobby-players v-if="players.length" :game="game" :players="players" />
        <v-btn
          class="mt-4"
          x-large
          color="accent"
          :disabled="players.length < 5"
          @click="startGame"
          >{{ t("Start game") }}</v-btn
        >
      </v-col>
      <v-col cols="12" md="3" xl="2">
        <v-card>
          <v-card-text>
            <qrcode
              :options="{
                size: 1000,
                background: '#fff',
                foreground: '#091619'
              }"
              :value="location"
            ></qrcode>
            <v-btn
              @click="copyText(location)"
              block
              class="mt-4 accent--text"
              text
            >
              {{ t("Copy game url") }}
            </v-btn>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>
    <v-snackbar v-model="snackbar" top :timeout="3000">
      {{ t("URL Copied") }}
      <v-btn dark text @click="snackbar = false">
        {{ t("Close") }}
      </v-btn>
    </v-snackbar>
  </v-container>
</template>

<script>
import LobbyPlayers from "./LobbyPlayers";
import { playersByIndex } from "@/utils/game";

async function copyTextToClipboard(text) {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textArea = document.createElement("textarea");
  textArea.style.position = "fixed";
  textArea.style.top = 0;
  textArea.style.left = 0;
  textArea.style.width = "2em";
  textArea.style.height = "2em";
  textArea.style.padding = 0;
  textArea.style.border = "none";
  textArea.style.outline = "none";
  textArea.style.boxShadow = "none";
  textArea.style.background = "transparent";
  textArea.value = text;
  document.body.appendChild(textArea);
  textArea.focus();
  textArea.select();
  document.execCommand("copy");
  document.body.removeChild(textArea);
}

export default {
  name: "Home",
  locales: {
    pt_br: {
      "Lobby for room": "Lobby para a sala",
      "Waiting for players": "Esperando pelos jogadores",
      "No players joined yet.": "Nenhum jogador entrou ainda.",
      "player joined.": "jogador entrou.",
      "players joined.": "jogadores entraram.",
      "URL Copied": "URL Copiada",
      Close: "Fechar",
      "Copy game url": "Copiar url do jogo ",
      "Join game": "Entrar em um jogo",
      "Start game": "Começar jogo"
    }
  },
  components: { LobbyPlayers },
  data: () => ({
    snackbar: false
  }),
  computed: {
    game() {
      return this.$store.state.game;
    },
    location() {
      return `${window.location.origin}/join?room=${this.game.gameId}`;
    },
    players() {
      return playersByIndex(this.game);
    },
    playerCount() {
      if (!this.players.length) return this.t("No players joined yet.");
      if (this.players.length === 1) {
        return `${this.players.length} ${this.t("player joined.")}`;
      }
      return `${this.players.length} ${this.t("players joined.")}`;
    }
  },
  methods: {
    async startGame() {
      await this.$store.dispatch("startGame", {
        gameId: this.game.gameId,
        detectiveIndex: this.game.detective
      });
    },
    async copyText(text) {
      await copyTextToClipboard(text);
      this.snackbar = true;
    }
  },
  async mounted() {
    await this.$store.dispatch("loadGame", this.$route.params.id);
    if (this.game?.lang) {
      this.$translate.setLang(this.game.lang);
    }
  }
};
</script>
<style lang="scss" scoped>
canvas {
  max-width: 100%;
}
</style>
