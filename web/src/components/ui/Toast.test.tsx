// Toasts: a keyed toast replaces the one before it (R10: pressing T three times stacked three
// theme toasts); unkeyed ones stack as before.
import { act, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ToastProvider } from "./Toast";
import { useToast, type ToastApi } from "./toast-context";

function Capture({ onApi }: { onApi: (api: ToastApi) => void }) {
  onApi(useToast());
  return null;
}

describe("ToastProvider", () => {
  it("replaces a toast with the same key and stacks the rest", () => {
    let api: ToastApi | null = null;
    render(
      <ToastProvider>
        <Capture onApi={(value) => (api = value)} />
      </ToastProvider>,
    );
    act(() => {
      api!.show({ title: "Theme: Phosphor", key: "theme" });
      api!.show({ title: "Theme: Deep sea", key: "theme" });
      api!.show({ title: "Theme: Ember", key: "theme" });
    });
    expect(screen.queryByText("Theme: Phosphor")).toBeNull();
    expect(screen.queryByText("Theme: Deep sea")).toBeNull();
    expect(screen.getByText("Theme: Ember")).toBeInTheDocument();
    act(() => {
      api!.show({ title: "Job j_1 given" });
      api!.show({ title: "Job j_2 given" });
    });
    expect(screen.getByText("Job j_1 given")).toBeInTheDocument();
    expect(screen.getByText("Job j_2 given")).toBeInTheDocument();
  });
});
