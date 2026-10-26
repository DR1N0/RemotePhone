import React from "react"
import ReactDOM from "react-dom/client"
import { ConfigProvider } from "antd"
import { BrowserRouter } from "react-router-dom"
import App from "./App.js"
import { adminTheme } from "./theme.js"
import "./admin.css"

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ConfigProvider theme={adminTheme}>
      <BrowserRouter basename="/admin">
        <App />
      </BrowserRouter>
    </ConfigProvider>
  </React.StrictMode>,
)
