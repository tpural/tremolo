import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useParams } from "react-router";
import { Layout } from "./components/Layout";
import "./index.css";
import { DashboardPage } from "./pages/DashboardPage";
import { LogsPage } from "./pages/LogsPage";
import { HostPage } from "./pages/HostPage";
import { Overview } from "./pages/Overview";
import { TracePage } from "./pages/TracePage";
import { AlertsPage, HistoryPage, NotificationsPage, RulesPage, SilencesPage } from "./pages/AlertsPage";
import { TracesPage } from "./pages/TracesPage";
import { LoginPage } from "./pages/LoginPage";
import { AckPage } from "./pages/AckPage";
import { RulePage } from "./pages/RulePage";
import { MaintenancePage } from "./pages/MaintenancePage";
import { RunbookPage, RunbooksPage } from "./pages/RunbooksPage";
import { StoragePage } from "./pages/StoragePage";
import { FrontendAppPage, FrontendPage } from "./pages/FrontendPage";
import { AgentRunPage, AgentRunsPage } from "./pages/AgentRunsPage";
import { ProfilePage } from "./pages/ProfilePage";
import { ActivityPage, UsersPage } from "./pages/UsersPage";
import { AgentsPage, InvestigationPage, InvestigationsPage } from "./pages/InvestigationsPage";
import { LegacyAck } from "./pages/LegacyAck";

/** OldRule sends links from before Rules had its own page. */
function OldRule() {
  const { name = "" } = useParams();
  return <Navigate to={`/rules/${encodeURIComponent(name)}${location.search}`} replace />;
}

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
              <Route path="alerts/silences" element={<SilencesPage />} />
              <Route path="alerts/history" element={<HistoryPage />} />
              <Route path="alerts/rules/:name" element={<OldRule />} />
              <Route path="rules" element={<RulesPage />} />
              <Route path="rules/:name" element={<RulePage />} />
              <Route path="notifications" element={<NotificationsPage />} />
              <Route path="activity" element={<ActivityPage />} />
              <Route path="investigations" element={<InvestigationsPage />} />
              <Route path="investigations/:id" element={<InvestigationPage />} />
              <Route path="agents" element={<AgentsPage />} />
              <Route path="agents/runs" element={<AgentRunsPage />} />
              <Route path="agents/runs/:trace" element={<AgentRunPage />} />
              <Route path="maintenance" element={<MaintenancePage />} />
              <Route path="runbooks" element={<RunbooksPage />} />
              <Route path="runbooks/:id" element={<RunbookPage />} />
              <Route path="storage" element={<StoragePage />} />
              <Route path="frontend" element={<FrontendPage />} />
              <Route path="frontend/:app" element={<FrontendAppPage />} />
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
