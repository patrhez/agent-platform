import { beforeEach, describe, expect, it, vi } from "vitest";
import { readSidebarOpen, SIDEBAR_OPEN_KEY, writeSidebarOpen } from "./sidebar";

describe("sidebar preference", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("defaults to open when unset", () => {
    expect(readSidebarOpen()).toBe(true);
  });

  it("defaults to open when invalid", () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "maybe");
    expect(readSidebarOpen()).toBe(true);
  });

  it("reads false when stored as false", () => {
    localStorage.setItem(SIDEBAR_OPEN_KEY, "false");
    expect(readSidebarOpen()).toBe(false);
  });

  it("writes string true/false", () => {
    writeSidebarOpen(false);
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("false");
    writeSidebarOpen(true);
    expect(localStorage.getItem(SIDEBAR_OPEN_KEY)).toBe("true");
  });

  it("returns true when getItem throws", () => {
    const getItem = vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
      throw new DOMException("The operation is insecure.", "SecurityError");
    });

    expect(readSidebarOpen()).toBe(true);

    getItem.mockRestore();
  });

  it("does not throw when setItem throws", () => {
    const setItem = vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("The operation is insecure.", "SecurityError");
    });

    expect(() => writeSidebarOpen(false)).not.toThrow();

    setItem.mockRestore();
  });
});
