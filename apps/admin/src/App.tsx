import { Routes, Route, Navigate } from "react-router-dom"
import Dashboard from "./pages/Dashboard.js"
import Session from "./pages/Session.js"

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/session/:sessionId" element={<Session />} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
