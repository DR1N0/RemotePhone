import type { ThemeConfig } from "antd"
import { theme } from "antd"

export const adminTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    colorPrimary: "#D9B45B",
    colorInfo: "#D9B45B",
    colorSuccess: "#D9B45B",
    colorWarning: "#D9B45B",
    colorError: "#E07A72",
    colorBgBase: "#000000",
    colorBgContainer: "#0D0D0D",
    colorBgElevated: "#151515",
    colorBorder: "#262626",
    colorBorderSecondary: "#1D1D1D",
    colorText: "#F4F3EF",
    colorTextSecondary: "#8B8B84",
    colorTextTertiary: "#6F6F69",
    borderRadius: 12,
    fontFamily: "'Segoe UI', 'Helvetica Neue', Arial, sans-serif",
    fontSize: 14,
    controlHeight: 40,
  },
  components: {
    Layout: { headerBg: "#000000", bodyBg: "#000000", siderBg: "#000000" },
    Card: { colorBgContainer: "#0D0D0D" },
    Table: { headerBg: "#0D0D0D", headerColor: "#8B8B84", rowHoverBg: "#151515" },
    Modal: { contentBg: "#0D0D0D", headerBg: "#0D0D0D" },
    Button: { primaryShadow: "none" },
    Input: { activeBorderColor: "#D9B45B", hoverBorderColor: "#8C7438" },
    Select: { optionSelectedBg: "#1D1D1D" },
  },
}
