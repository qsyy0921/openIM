import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";

import { ChannelWorkspace } from "./ChannelWorkspace";
import { TelegramLinkController, type TelegramLinkState } from "./telegram-link";

function controller(): TelegramLinkController {
  return new TelegramLinkController({ status: vi.fn(), issue: vi.fn() });
}

describe("ChannelWorkspace", () => {
  it("renders a pending challenge expiry and one-time command", () => {
    const state: TelegramLinkState = {
      status: "pending",
      expiresAt: "2030-01-01T00:05:00Z",
      challenge: {
        state: "pending",
        code: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
        command: "/link ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567",
        expires_at: "2030-01-01T00:05:00Z"
      },
      loading: false,
      issuing: false,
      error: null
    };
    const html = renderToStaticMarkup(createElement(ChannelWorkspace, { controller: controller(), state }));
    expect(html).toContain("等待 Telegram 确认");
    expect(html).toContain("/link ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567");
  });

  it("does not redisplay plaintext after a page refresh loses the issuing response", () => {
    const state: TelegramLinkState = {
      status: "pending", expiresAt: "2030-01-01T00:05:00Z", challenge: null,
      loading: false, issuing: false, error: null
    };
    const html = renderToStaticMarkup(createElement(ChannelWorkspace, { controller: controller(), state }));
    expect(html).toContain("当前绑定码不再显示");
    expect(html).not.toContain("<code>");
  });
});
