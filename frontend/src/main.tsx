import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AuthProvider } from "react-oidc-context";

import App from "./App";
import { oidcConfig } from "./auth/oidc";
import { AuthGuard } from "./auth/AuthGuard";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider {...oidcConfig}>
      <AuthGuard>
        <App />
      </AuthGuard>
    </AuthProvider>
  </StrictMode>,
);
