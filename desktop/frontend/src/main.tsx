import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { ToastProvider } from "./ui";
import "./styles.css";

// In a plain browser during development, stand in for the Go engine.
const ready = import.meta.env.DEV && !window.go ? import("./devmock") : Promise.resolve();

// Without this, Wails never intercepts drops and WebView2 saves the dragged file to Downloads. Go receives the paths.
ready.then(() => window.runtime?.OnFileDrop?.(() => {}, false));

ready.then(() =>
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <ToastProvider>
        <App />
      </ToastProvider>
    </StrictMode>,
  ),
);
