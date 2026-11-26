// Talk-mode buttons for the Inbox (DESIGN.md 8.1, 13), shown only when the server has voice on:
// "Read aloud" speaks a question's title and options through the same server-held text ids as
// every other spoken line (the server builds the text; the browser only gets an id), and the
// dictate button records into the answer's note field. Neither needs talk mode switched on: the
// press itself is the gesture that lets audio play and the microphone open.
import { Loader2, Mic, Square, Volume2 } from "lucide-react";
import { useEffect, useState } from "react";
import { endpoints } from "../../lib/api";
import { player } from "../../lib/voice/player";
import { usePushToTalk } from "../../lib/voice/recorder";
import { insecureNotice, micSupport } from "../../lib/voice/support";
import { Button } from "../ui/Button";
import { useToast } from "../ui/toast-context";

export function ReadAloudButton({ questionId }: { questionId: string }) {
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const read = () => {
    player.unlock(); // synchronously inside the click, before any await
    setBusy(true);
    endpoints
      .readAloud(questionId)
      .then(
        (result) => player.playNow({ textId: result.text_id, text: "Reading the question aloud" }),
        (error: unknown) => toast.show({ tone: "danger", title: "Could not read it aloud", description: error instanceof Error ? error.message : undefined }),
      )
      .finally(() => setBusy(false));
  };
  return (
    <Button compact variant="ghost" icon={busy ? Loader2 : Volume2} onClick={read} disabled={busy}>
      Read aloud
    </Button>
  );
}

/** Tap to record, tap again to stop; the transcript is appended through onText. */
export function DictateButton({ onText }: { onText: (text: string) => void }) {
  const toast = useToast();
  const ptt = usePushToTalk(
    async (text) => {
      onText(text);
    },
    { review: false },
  );
  const { state } = ptt;
  const recording = state.phase === "recording" || state.phase === "starting";
  const busy = state.phase === "uploading" || state.phase === "starting";

  const { dismiss } = ptt;
  const errorMessage = state.phase === "error" ? state.message : null;
  useEffect(() => {
    if (errorMessage === null) return;
    toast.show({ tone: "danger", title: "Dictation failed", description: errorMessage });
    dismiss();
  }, [errorMessage, dismiss, toast]);

  const toggle = () => {
    const support = micSupport();
    if (support !== "ok") {
      toast.show({ tone: "warning", title: "No microphone here", description: support === "insecure" ? insecureNotice : "This browser cannot record audio." });
      return;
    }
    // A press then an immediate release is a tap, which locks recording on; the next press stops it.
    ptt.press();
    if (!recording) ptt.release();
  };

  return (
    <Button
      compact
      variant={recording ? "danger" : "ghost"}
      icon={busy ? Loader2 : recording ? Square : Mic}
      onClick={toggle}
      disabled={state.phase === "uploading"}
      aria-pressed={recording}
      aria-label={recording ? "Stop dictating" : "Dictate the note"}
    >
      {recording ? "Stop" : "Dictate"}
    </Button>
  );
}
