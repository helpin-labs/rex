import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ConfirmHost } from "./confirm";
import { applyTheme, loadTheme } from "./theme";
import "@fontsource-variable/inter";
import "./fonts.css";
import "./styles.css";

applyTheme(loadTheme());

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
    <ConfirmHost />
  </React.StrictMode>,
);
