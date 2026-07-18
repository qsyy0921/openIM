import { ApplicationHandleResult, type FriendApplicationItem, type FriendUserItem, type PublicUserItem } from "@openim/wasm-client-sdk";
import { describe, expect, it } from "vitest";

import { ContactController, type ContactEvents, type ContactPort } from "./contact";

function friend(userID: string, nickname = userID): FriendUserItem {
  return { userID, nickname, remark: "", faceURL: "" } as FriendUserItem;
}

function application(fromUserID: string, toUserID: string, handleResult = ApplicationHandleResult.Unprocessed): FriendApplicationItem {
  return { fromUserID, toUserID, handleResult, createTime: 1, reqMsg: "hello" } as FriendApplicationItem;
}

class FakeContactPort implements ContactPort {
  friendItems: FriendUserItem[] = [];
  incomingItems: FriendApplicationItem[] = [];
  outgoingItems: FriendApplicationItem[] = [];
  lookup: (userID: string) => Promise<PublicUserItem | null> = async (userID) => ({ userID, nickname: userID, faceURL: "", ex: "" });
  events: ContactEvents | null = null;
  addCalls = 0;
  acceptCalls: string[] = [];
  rejectCalls: string[] = [];
  addAction: () => Promise<void> = async () => undefined;

  friends = async () => this.friendItems;
  incomingApplications = async () => this.incomingItems;
  outgoingApplications = async () => this.outgoingItems;
  lookupUser = (userID: string) => this.lookup(userID);
  addFriend = async () => { this.addCalls += 1; await this.addAction(); };
  acceptApplication = async (userID: string) => { this.acceptCalls.push(userID); };
  rejectApplication = async (userID: string) => { this.rejectCalls.push(userID); };
  subscribe = (events: ContactEvents) => {
    this.events = events;
    return () => { this.events = null; };
  };
}

describe("ContactController", () => {
  it("loads and deduplicates friends and applications", async () => {
    const port = new FakeContactPort();
    port.friendItems = [friend("friend-1"), friend("friend-1"), friend("self")];
    port.incomingItems = [application("friend-2", "self"), application("friend-2", "self")];
    const controller = new ContactController(port);

    await controller.start("self");

    expect(controller.getState().friends.map((item) => item.userID)).toEqual(["friend-1"]);
    expect(controller.getState().incomingApplications).toHaveLength(1);
    expect(controller.getState()).toMatchObject({ loading: false, error: null });
  });

  it("discards stale exact-user lookup results", async () => {
    const port = new FakeContactPort();
    let resolveFirst!: (user: PublicUserItem) => void;
    const first = new Promise<PublicUserItem>((resolve) => { resolveFirst = resolve; });
    port.lookup = (userID) => userID === "old-user" ? first : Promise.resolve({ userID, nickname: "New", faceURL: "", ex: "" });
    const controller = new ContactController(port);
    await controller.start("self");

    const oldLookup = controller.lookup("old-user");
    await controller.lookup("new-user");
    resolveFirst({ userID: "old-user", nickname: "Old", faceURL: "", ex: "" });
    await oldLookup;

    expect(controller.getState().lookupResult).toMatchObject({ userID: "new-user" });
  });

  it("coalesces callback refreshes and keeps the authoritative projection unique", async () => {
    const port = new FakeContactPort();
    const controller = new ContactController(port);
    await controller.start("self");
    port.friendItems = [friend("friend-1"), friend("friend-1")];

    port.events?.changed();
    port.events?.changed();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(controller.getState().friends.map((item) => item.userID)).toEqual(["friend-1"]);
  });

  it("rejects a duplicate mutation while the first SDK operation is pending", async () => {
    const port = new FakeContactPort();
    let release!: () => void;
    port.addAction = () => new Promise<void>((resolve) => { release = resolve; });
    const controller = new ContactController(port);
    await controller.start("self");

    const first = controller.addFriend("friend-1", "hello");
    await expect(controller.addFriend("friend-1", "hello")).rejects.toThrow("正在处理中");
    release();
    await first;

    expect(port.addCalls).toBe(1);
    expect(controller.getState().pendingOperations).toEqual([]);
  });

  it("accepts and rejects incoming applications through the exact applicant ID", async () => {
    const port = new FakeContactPort();
    const controller = new ContactController(port);
    await controller.start("self");

    await controller.acceptApplication("friend-1");
    await controller.rejectApplication("friend-2");

    expect(port.acceptCalls).toEqual(["friend-1"]);
    expect(port.rejectCalls).toEqual(["friend-2"]);
  });

  it("invalidates pending lookup and removes SDK listeners on stop", async () => {
    const port = new FakeContactPort();
    let resolveLookup!: (user: PublicUserItem) => void;
    port.lookup = () => new Promise<PublicUserItem>((resolve) => { resolveLookup = resolve; });
    const controller = new ContactController(port);
    await controller.start("self");
    const lookup = controller.lookup("friend-1");

    controller.stop();
    resolveLookup({ userID: "friend-1", nickname: "Friend", faceURL: "", ex: "" });
    await lookup;

    expect(port.events).toBeNull();
    expect(controller.getState().lookupResult).toBeNull();
  });
});
