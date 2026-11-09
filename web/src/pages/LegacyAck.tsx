import { Navigate, Outlet, useSearchParams } from "react-router";

/** LegacyAck sends links from older notifications, /alerts?ack=…, to the ack page. */
export function LegacyAck() {
  const [params] = useSearchParams();
  const token = params.get("ack");
  return token ? <Navigate to={`/ack/${encodeURIComponent(token)}`} replace /> : <Outlet />;
}
