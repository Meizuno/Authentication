export default defineEventHandler(async (event) => {
  const config = useRuntimeConfig();
  const query = getQuery(event);

  const code = query.code as string;
  if (!code) {
    throw createError({ statusCode: 400, statusMessage: "No code" });
  }

  const tokenRes = await $fetch<any>("https://oauth2.googleapis.com/token", {
    method: "POST",
    body: {
      client_id: config.public.googleClientId,
      client_secret: config.googleClientSecret,
      code,
      grant_type: "authorization_code",
      redirect_uri: config.public.googleRedirectUri,
    },
  });

  const user = await $fetch<{ email: string; name: string; picture: string }>(
    "https://www.googleapis.com/oauth2/v2/userinfo",
    {
      headers: {
        Authorization: `Bearer ${tokenRes.access_token}`,
      },
    }
  );

  await setUserSession(event, {
    user: {
      name: user.name,
      email: user.email,
      photo: user.picture,
    },
  });

  return sendRedirect(event, "/");
});
