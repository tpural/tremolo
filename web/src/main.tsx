import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Route, Routes } from "react-router";
import { Layout } from "./components/Layout";
import "./index.css";
import { DashboardPage } from "./pages/DashboardPage";
import { LogsPage } from "./pages/LogsPage";
import { HostPage } from "./pages/HostPage";
import { Overview } from "./pages/Overview";
import { TracePage } from "./pages/TracePage";
import { AlertsPage } from "./pages/AlertsPage";
import { TracesPage } from "./pages/TracesPage";
import { LoginPage } from "./pages/LoginPage";
import { AckPage } from "./pages/AckPage";
import { ProfilePage } from "./pages/ProfilePage";
import { UsersPage } from "./pages/UsersPage";
import { LegacyAck } from "./pages/LegacyAck";

const client = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } } });

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <BrowserRouter>
        <Routes>
          <Route path="login" element={<LoginPage />} />
          <Route path="ack/:token" element={<AckPage />} />
          <Route element={<LegacyAck />}>
            <Route element={<Layout />}>
              <Route index element={<Overview />} />
              <Route path="hosts/:name" element={<HostPage />} />
              <Route path="d/:id" element={<DashboardPage />} />
              <Route path="logs" element={<LogsPage />} />
              <Route path="traces" element={<TracesPage />} />
              <Route path="traces/:id" element={<TracePage />} />
              <Route path="alerts" element={<AlertsPage />} />
              <Route path="profile" element={<ProfilePage />} />
              <Route path="users" element={<UsersPage />} />
              <Route path="*" element={<p className="text-muted">Not found.</p>} />
            </Route>
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
