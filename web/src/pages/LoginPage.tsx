import { useMutation, useQueryClient } from "@tanstack/react-query";
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
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const login = useMutation({
    mutationFn: () => api.login(name, password),
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
        <label className="block space-y-1 text-sm">
          <span className="text-muted">Name</span>
          <input
            autoFocus
            autoComplete="username"
            autoCapitalize="none"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full rounded-md border border-line bg-bg px-2.5 py-1.5"
          />
        </label>
        <label className="block space-y-1 text-sm">
          <span className="text-muted">Password</span>
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full rounded-md border border-line bg-bg px-2.5 py-1.5"
          />
        </label>
        {login.error && <p className="text-sm text-bad">{login.error.message}</p>}
        <button
          type="submit"
          disabled={login.isPending || !name || !password}
          className="w-full rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-white disabled:opacity-60 dark:text-bg"
        >
          {login.isPending ? "Signing in…" : "Sign in"}
        </button>
      </form>
    </div>
  );
}
