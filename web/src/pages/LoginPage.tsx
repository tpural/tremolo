import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { api } from "../lib/api";

/** safeNext keeps redirects after signing in on this site. */
export function safeNext(next: string | null) {
  return next && next.startsWith("/") && !next.startsWith("//") ? next : "/";
}

export function LoginPage() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setupNeeded, retry: false });
  const first = setup.data?.needed ?? false;
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const mismatch = first && confirm !== "" && confirm !== password;
  const login = useMutation({
    mutationFn: () => (first ? api.setup(name || "admin", password) : api.login(name, password)),
    onSuccess: (me) => {
      client.clear();
      client.setQueryData(["me"], me);
      navigate(safeNext(params.get("next")), { replace: true });
    },
  });
  return (
    <div className="flex min-h-dvh items-center justify-center bg-bg px-4">
      <form
        className="w-full max-w-sm space-y-4 rounded-2xl border border-line bg-panel p-6"
        onSubmit={(e) => {
          e.preventDefault();
          login.mutate();
        }}
      >
        <div className="flex items-center gap-2">
          <img src="/loon-calling.png" alt="" className="h-8" />
          <span className="text-xl font-semibold tracking-tight">tremolo</span>
        </div>
        {first && <p className="text-sm text-muted">Welcome. Create the admin account; you'll add everyone else from Users and teams.</p>}
        <label className="block space-y-1 text-sm">
          <span className="text-muted">Name</span>
          <input
            autoFocus
            autoComplete="username"
            autoCapitalize="none"
            placeholder={first ? "admin" : undefined}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded-md border border-line-strong/70 bg-panel px-2.5 py-1.5"
          />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="text-muted">Password</span>
          <input
            type="password"
            autoComplete={first ? "new-password" : "current-password"}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md border border-line-strong/70 bg-panel px-2.5 py-1.5"
          />
        </label>
        {first && (
          <label className="block space-y-1 text-sm">
            <span className="text-muted">Password again</span>
            <input
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="w-full rounded-md border border-line-strong/70 bg-panel px-2.5 py-1.5"
            />
          </label>
        )}
        {mismatch && <p className="text-sm text-bad">The passwords don't match.</p>}
        {login.error && <p className="text-sm text-bad">{login.error.message}</p>}
        <button
          type="submit"
          disabled={login.isPending || (!first && !name) || !password || (first && confirm !== password) || setup.isLoading}
          className="w-full rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-on-accent disabled:opacity-60"
        >
          {first ? (login.isPending ? "Creating…" : "Create admin and sign in") : login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
