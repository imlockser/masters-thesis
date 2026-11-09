import { App as AntApp, ConfigProvider, theme } from "antd";
import { useState } from "react";
import { ChatPage } from "./chat/ChatPage";

const themeKey = "energy-assistant:theme";

export default function App() {
  const [dark, setDark] = useState(() => {
    try {
      return localStorage.getItem(themeKey) === "dark";
    } catch {
      return false;
    }
  });

  const borderColor = dark ? "#1d1d1d" : "#fafafa";

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    try {
      localStorage.setItem(themeKey, next ? "dark" : "light");
    } catch {
      // Theme switching remains available when browser storage is blocked.
    }
  }

  return (
    <ConfigProvider wave={{ disabled: true }} theme={{
      algorithm: dark ? theme.darkAlgorithm : theme.defaultAlgorithm,
      token: {
        borderRadius: 8,
        colorBorder: borderColor,
        colorBorderSecondary: borderColor,
      },
      components: {
        Button: {
          defaultShadow: "none",
          primaryShadow: "none",
          dangerShadow: "none",
        },
      },
    }}>
      <AntApp style={{ colorScheme: dark ? "dark" : "light" }}>
        <ChatPage dark={dark} onToggleTheme={toggleTheme} />
      </AntApp>
    </ConfigProvider>
  );
}
