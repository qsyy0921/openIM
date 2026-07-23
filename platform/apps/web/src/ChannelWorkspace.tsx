import { AlertCircle, Check, Copy, Link2, RefreshCw, Send, ShieldCheck, X } from "lucide-react";
import { useState } from "react";

import type { TelegramLinkController, TelegramLinkState } from "./telegram-link";

type Props = { controller: TelegramLinkController; state: TelegramLinkState };

function formatExpiry(value: string | null): string {
  if (!value) return "";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(date);
}

export function ChannelWorkspace({ controller, state }: Props) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState(false);

  const issue = async () => {
    setCopied(false);
    setCopyError(false);
    try {
      await controller.issue();
    } catch {
      // TelegramLinkState exposes the exact operation failure.
    }
  };

  const copyCommand = async () => {
    if (!state.challenge) return;
    try {
      await navigator.clipboard.writeText(state.challenge.command);
      setCopied(true);
      setCopyError(false);
    } catch {
      setCopied(false);
      setCopyError(true);
    }
  };

  return (
    <section className="channel-layout" aria-labelledby="channel-title">
      <header className="channel-header">
        <div><h1 id="channel-title">渠道连接</h1><p>管理企业身份与外部协作渠道的授权连接</p></div>
        <button className="subtle-icon-button" title="刷新连接状态" aria-label="刷新连接状态" disabled={state.loading || state.issuing} onClick={() => void controller.refresh().catch(() => undefined)}>
          <RefreshCw size={17} className={state.loading ? "spin" : undefined} />
        </button>
      </header>

      {state.error && <div className="channel-error" role="alert"><AlertCircle size={17} /><span>{state.error}</span><button onClick={() => controller.clearError()} aria-label="关闭错误"><X size={15} /></button></div>}

      <div className="channel-scroll">
        <div className="channel-security"><ShieldCheck size={19} /><div><strong>双端身份校验</strong><span>企业登录确认成员身份，Telegram 私聊确认渠道身份。绑定命令不会进入 Agent 对话。</span></div></div>

        <section className="channel-row" aria-label="Telegram 连接">
          <span className="channel-icon"><Send size={21} /></span>
          <div className="channel-copy"><strong>Telegram</strong><span>企业知识问答与 Agent 通知渠道</span></div>
          <span className={`channel-state ${state.status}`}>
            {state.status === "bound" ? <><Check size={15} />已连接</> : state.status === "pending" ? <><RefreshCw size={15} />等待确认</> : state.loading || state.status === "idle" ? <><RefreshCw className="spin" size={15} />读取中</> : <><Link2 size={15} />未连接</>}
          </span>
          <div className="channel-action">
            {state.status === "bound" ? <span className="channel-bound-note">连接已由服务端验证</span> : (
              <button className="primary-button compact" disabled={state.issuing || state.loading} onClick={() => void issue()}>
                {state.issuing ? <RefreshCw className="spin" size={15} /> : <Link2 size={15} />}{state.status === "pending" ? "生成新绑定码" : "连接"}
              </button>
            )}
          </div>
        </section>

        {state.status === "pending" && (
          <section className="link-command-panel" aria-live="polite">
            <div className="link-command-heading"><div><strong>等待 Telegram 确认</strong><span>有效期至 {formatExpiry(state.expiresAt)}</span></div><RefreshCw className="spin" size={18} /></div>
            {state.challenge ? (
              <div className="link-command-row"><code>{state.challenge.command}</code><button className="secondary-button compact" onClick={() => void copyCommand()}>{copied ? <Check size={15} /> : <Copy size={15} />}{copied ? "已复制" : "复制"}</button></div>
            ) : <p>当前绑定码不再显示。可生成新绑定码并使旧码立即失效。</p>}
            {copyError && <p className="link-copy-error" role="alert">复制失败，请手动选择命令。</p>}
            <small>在 Telegram 机器人私聊中发送该命令，页面会自动更新连接状态。</small>
          </section>
        )}
      </div>
    </section>
  );
}
