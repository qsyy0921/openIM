import { LogIn, MessageSquare } from "lucide-react";
import { useState } from "react";
import type { UserManager } from "oidc-client-ts";

type SignedOutAppProps = {
  userManager: UserManager;
};

export function SignedOutApp({ userManager }: SignedOutAppProps) {
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const login = async () => {
    setError(null);
    setSubmitting(true);
    try {
      await userManager.signinRedirect();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "enterprise login failed");
      setSubmitting(false);
    }
  };

  return (
    <main className="auth-shell">
      <section className="auth-panel" aria-labelledby="sign-in-title">
        <div className="brand-mark"><MessageSquare size={22} /></div>
        <div>
          <p className="eyebrow">OPENIM WORKSPACE</p>
          <h1 id="sign-in-title">企业协作台</h1>
        </div>
        {error && <p className="error-text" role="alert">{error}</p>}
        <button className="primary-button" disabled={submitting} onClick={() => void login()}>
          <LogIn size={18} />{submitting ? "正在跳转" : "企业登录"}
        </button>
      </section>
    </main>
  );
}
