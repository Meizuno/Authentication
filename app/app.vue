<template>
  <button @click="loginWithGoogle">Google</button>
</template>

<script setup lang="ts">
const config = useRuntimeConfig();
const loginWithGoogle = () => {
  window.location.href =
    `https://accounts.google.com/o/oauth2/v2/auth` +
    `?client_id=${config.public.googleClientId}` +
    `&redirect_uri=${config.public.googleRedirectUri}` +
    `&response_type=code` +
    `&scope=openid email profile`;
};
</script>
