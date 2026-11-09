import { useEffect, useRef, type ReactNode } from "react";
import { useAuth } from "react-oidc-context";

interface AuthGuardProps {
  children: ReactNode;
}

export function AuthGuard({ children }: AuthGuardProps) {
  const auth = useAuth();
  const redirectStarted = useRef(false);

  useEffect(() => {
    if (
      !auth.isLoading &&
      !auth.isAuthenticated &&
      !auth.activeNavigator &&
      !auth.error &&
      !redirectStarted.current &&
      !window.location.search.includes("code=")
    ) {
      redirectStarted.current = true;

      void auth.signinRedirect().catch(() => {
        redirectStarted.current = false;
      });
    }
  }, [auth]);

  if (auth.isLoading || auth.activeNavigator) {
    return <p>Checking authentication...</p>;
  }

  if (auth.error) {
    return <p>Authentication error: {auth.error.message}</p>;
  }

  if (!auth.isAuthenticated) {
    return <p>Redirecting to ZITADEL...</p>;
  }

  if (!auth.user?.access_token) {
    return <p>Authentication error: access token is unavailable.</p>;
  }

  return <>{children}</>;
}

