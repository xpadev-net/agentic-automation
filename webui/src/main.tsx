import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./styles.css";

// /runs/<id> links (from GitHub comments) land on the SPA fallback with a
// clean path; translate them to the hash router's form on boot.
if (/^\/runs\/\d+/.test(window.location.pathname)) {
  window.location.replace(`/#${window.location.pathname}`);
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
