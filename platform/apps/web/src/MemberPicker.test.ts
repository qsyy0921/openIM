import type { ContactState } from "./contact";
import { memberCandidates, toggleMemberSelection } from "./MemberPicker";
import { describe, expect, it } from "vitest";

describe("MemberPicker utilities", () => {
  it("deduplicates friends and lookup results while excluding self", () => {
    const state = {
      friends: [
        { userID: "friend-1", nickname: "Friend", remark: "", faceURL: "" },
        { userID: "self", nickname: "Self", remark: "", faceURL: "" }
      ],
      lookupResult: { userID: "friend-1", nickname: "Duplicate", faceURL: "", ex: "" },
      lookedUpUsers: [
        { userID: "friend-1", nickname: "Duplicate", faceURL: "", ex: "" },
        { userID: "lookup-2", nickname: "Lookup", faceURL: "", ex: "" }
      ]
    } as ContactState;

    expect(memberCandidates(state, "self")).toEqual([
      { userID: "friend-1", nickname: "Friend", faceURL: "", source: "friend" },
      { userID: "lookup-2", nickname: "Lookup", faceURL: "", source: "lookup" }
    ]);
  });

  it("toggles one unique member ID without introducing duplicates", () => {
    expect(toggleMemberSelection(["friend-1", "friend-1"], "friend-2")).toEqual(["friend-1", "friend-2"]);
    expect(toggleMemberSelection(["friend-1", "friend-2"], "friend-1")).toEqual(["friend-2"]);
  });
});
