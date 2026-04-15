<template>
  <v-card class="mt-4">
    <v-slide-y-transition group>
      <v-list-item two-line v-for="player in players" :key="player.playerkey">
        <v-list-item-content>
          <v-list-item-title class="font-weight-bold">{{
            player.name
          }}</v-list-item-title>
          <v-list-item-subtitle>
            /game/{{ game.gameId }}/player/{{ player.slug }}
          </v-list-item-subtitle>
        </v-list-item-content>
        <v-list-item-action>
          <v-btn icon @click="makeDetective(player.index)">
            <v-icon
              :color="player.index === game.detective ? 'secondary' : 'gray'"
              >mdi-police-badge</v-icon
            >
          </v-btn>
        </v-list-item-action>
      </v-list-item>
    </v-slide-y-transition>
  </v-card>
</template>

<script>
export default {
  props: {
    players: {
      type: Array,
      required: true
    },
    game: {
      type: Object,
      required: true
    }
  },
  methods: {
    async makeDetective(playerIndex) {
      await this.$store.dispatch("setDetective", {
        gameId: this.game.gameId,
        player: playerIndex
      });
    }
  }
};
</script>

<style lang="scss" scoped></style>
