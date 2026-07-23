import { MessageStatus, MessageType, SessionType, type MessageItem, type SearchMessageResult } from "@openim/wasm-client-sdk";
import { describe, expect, it } from "vitest";

import { MESSAGE_SEARCH_PAGE_SIZE, MessageSearchController, type MessageSearchPort } from "./message-search";

function message(id: string, contentType = MessageType.TextMessage): MessageItem {
  return {
    clientMsgID: id,
    contentType,
    status: MessageStatus.Succeed,
    sessionType: SessionType.Single,
    sendID: "peer",
    recvID: "self",
    textElem: contentType === MessageType.TextMessage ? { content: id } : undefined,
    fileElem: contentType === MessageType.FileMessage ? { fileName: `${id}.txt` } : undefined
  } as MessageItem;
}

function result(conversationID: string, totalCount: number, messages: MessageItem[]): SearchMessageResult {
  return {
    totalCount,
    searchResultItems: [{
      conversationID,
      conversationType: SessionType.Single,
      showName: "Peer",
      faceURL: "",
      messageCount: messages.length,
      messageList: messages
    }]
  };
}

class FakePort implements MessageSearchPort {
  calls: Array<{ conversationID: string; query: string; page: number; count: number }> = [];
  response: SearchMessageResult = result("single", 0, []);
  error: Error | null = null;
  pending: Array<(value: SearchMessageResult) => void> = [];

  search = async (conversationID: string, query: string, page: number, count: number) => {
    this.calls.push({ conversationID, query, page, count });
    if (this.error) throw this.error;
    if (query.startsWith("defer-")) return new Promise<SearchMessageResult>((resolve) => this.pending.push(resolve));
    return this.response;
  };
}

describe("MessageSearchController", () => {
  it("submits a bounded conversation-scoped page and keeps supported hits", async () => {
    const port = new FakePort();
    port.response = result("single", 3, [message("text"), message("file", MessageType.FileMessage), message("picture", MessageType.PictureMessage)]);
    const controller = new MessageSearchController(port);
    controller.setQuery("  unique  ");

    await controller.search("single");

    expect(port.calls).toEqual([{ conversationID: "single", query: "unique", page: 1, count: MESSAGE_SEARCH_PAGE_SIZE }]);
    expect(controller.getState().hits.map((hit) => hit.message.clientMsgID)).toEqual(["text", "file"]);
    expect(controller.getState()).toMatchObject({ submittedQuery: "unique", page: 1, totalCount: 3, loading: false, error: null });
  });

  it("navigates official pages without crossing bounds", async () => {
    const port = new FakePort();
    port.response = result("single", 41, [message("hit")]);
    const controller = new MessageSearchController(port);
    await controller.search("single", "term");
    await controller.nextPage();
    await controller.previousPage();

    expect(port.calls.map((call) => call.page)).toEqual([1, 2, 1]);
    port.response = result("single", 20, [message("last")]);
    await controller.search("single", "term", 1);
    await controller.nextPage();
    expect(port.calls.at(-1)?.page).toBe(1);
  });

  it("prevents stale success and stale failure from replacing the latest request", async () => {
    const port = new FakePort();
    const controller = new MessageSearchController(port);
    const old = controller.search("single", "defer-old");
    const latest = controller.search("single", "defer-latest");
    port.pending[1](result("single", 1, [message("latest")]));
    await latest;
    port.pending[0](result("single", 1, [message("old")]));
    await old;

    expect(controller.getState().hits[0].message.clientMsgID).toBe("latest");
    expect(controller.getState().submittedQuery).toBe("defer-latest");
  });

  it("keeps errors explicit and rejects invalid input without calling the SDK", async () => {
    const port = new FakePort();
    const controller = new MessageSearchController(port);
    await expect(controller.search("", "term")).rejects.toThrow("请选择会话");
    await expect(controller.search("single", " ")).rejects.toThrow("请输入搜索关键词");
    expect(port.calls).toEqual([]);

    port.error = new Error("index unavailable");
    await expect(controller.search("single", "term")).rejects.toThrow("index unavailable");
    expect(controller.getState()).toMatchObject({ hits: [], totalCount: 0, loading: false, error: "index unavailable" });
  });

  it("cancels an in-flight request and clears all ephemeral state on close", async () => {
    const port = new FakePort();
    const controller = new MessageSearchController(port);
    const pending = controller.search("single", "defer-close");
    controller.close();
    port.pending[0](result("single", 1, [message("late")]));
    await pending;

    expect(controller.getState()).toMatchObject({ query: "", submittedQuery: "", conversationID: null, hits: [], loading: false });
  });
});
