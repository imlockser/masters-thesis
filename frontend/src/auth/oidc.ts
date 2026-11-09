import type { AuthProviderProps } from "react-oidc-context";

const issuer = import.meta.env.VITE_ZITADEL_ISSUER;
const clientId = import.meta.env.VITE_ZITADEL_CLIENT_ID;
const projectId = import.meta.env.VITE_ZITADEL_PROJECT_ID;

export const oidcConfig: AuthProviderProps = {
  authority: issuer,
  client_id: clientId,

  redirect_uri: `${window.location.origin}/auth/callback`,
  post_logout_redirect_uri: `${window.location.origin}/`,

  response_type: "code",

  scope: [
    "openid",
    "profile",
    "email",
    "offline_access",
    "urn:zitadel:iam:org:projects:roles",
    `urn:zitadel:iam:org:project:id:${projectId}:aud`,
  ].join(" "),

  onSigninCallback: () => {
    window.history.replaceState({}, document.title, "/");
  },
};
