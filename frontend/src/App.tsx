import { App as AntApp, ConfigProvider, theme } from "antd";
import { useLayoutEffect, useState } from "react";
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

  const primary = dark ? "#C6A0F6" : "#8839EF";
  const primaryTint = dark ? "#363A4F" : "#E9DFF7";
  const secondaryText = dark ? "#A5ADCB" : "#5C5F77";
  const actionText = dark ? "#181926" : "#FFFFFF";
  const background = dark ? "#24273A" : "#EFF1F5";
  const colorScheme = dark ? "dark" : "light";

  useLayoutEffect(() => {
    const rootStyle = document.documentElement.style;
    rootStyle.backgroundColor = background;
    rootStyle.colorScheme = colorScheme;
    return () => {
      rootStyle.removeProperty("background-color");
      rootStyle.removeProperty("color-scheme");
    };
  }, [background, colorScheme]);

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
        borderRadius: 10,
        colorPrimary: primary,
        colorPrimaryBg: primaryTint,
        colorLink: primary,
        colorBgLayout: background,
        colorBgContainer: dark ? "#292C41" : "#F9FAFB",
        colorBgElevated: dark ? "#363A4F" : "#F9FAFB",
        colorText: dark ? "#CAD3F5" : "#4C4F69",
        colorTextSecondary: secondaryText,
        colorTextDescription: secondaryText,
        colorTextPlaceholder: secondaryText,
        colorSuccess: dark ? "#A6DA95" : "#2C721F",
        colorSuccessBg: dark ? "#273729" : "#E6F0E3",
        colorInfo: dark ? "#8AADF4" : "#1955C5",
        colorInfoBg: dark ? "#2B3651" : "#E3EBFB",
        colorError: dark ? "#ED8796" : "#D20F39",
        colorErrorBg: dark ? "#412C3D" : "#F9E4E9",
        colorWarning: dark ? "#EED49F" : "#986000",
        colorBorder: dark ? "#5B6078" : "#ACB0BE",
        colorBorderSecondary: dark ? "#363A4F" : "#CCD0DA",
      },
      components: {
        Button: {
          primaryColor: actionText,
          colorPrimaryHover: dark ? primary : "#7833D4",
          dangerColor: actionText,
          defaultShadow: "none",
          primaryShadow: "none",
          dangerShadow: "none",
        },
        Menu: {
          itemSelectedBg: primaryTint,
        },
      },
    }}>
      <AntApp>
        <ChatPage dark={dark} onToggleTheme={toggleTheme} />
      </AntApp>
    </ConfigProvider>
  );
}
