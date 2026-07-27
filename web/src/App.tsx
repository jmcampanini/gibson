import { useEffect, useState } from "react";

type HealthState =
  | { status: "loading" }
  | { status: "ready"; version: string }
  | { status: "error" };

export default function App() {
  const [health, setHealth] = useState<HealthState>({ status: "loading" });

  useEffect(() => {
    const controller = new AbortController();

    async function loadHealth() {
      try {
        const response = await fetch("/api/health", {
          headers: { Accept: "application/json" },
          signal: controller.signal,
        });

        if (!response.ok) {
          throw new Error("Health request failed");
        }

        const result: unknown = await response.json();
        if (
          typeof result !== "object" ||
          result === null ||
          !("ok" in result) ||
          result.ok !== true ||
          !("version" in result) ||
          typeof result.version !== "string"
        ) {
          throw new Error("Invalid health response");
        }

        if (!controller.signal.aborted) {
          setHealth({ status: "ready", version: result.version });
        }
      } catch (error) {
        if (
          !controller.signal.aborted &&
          !(error instanceof DOMException && error.name === "AbortError")
        ) {
          setHealth({ status: "error" });
        }
      }
    }

    void loadHealth();
    return () => controller.abort();
  }, []);

  return (
    <main className="shell">
      <section className="panel" aria-labelledby="app-title">
        <p className="eyebrow">Agent workspace</p>
        <h1 id="app-title">Gibson</h1>
        {health.status === "loading" && (
          <p className="health" aria-live="polite">
            Connecting to server…
          </p>
        )}
        {health.status === "ready" && (
          <p className="health health--ready" aria-live="polite">
            Server version <strong>{health.version}</strong>
          </p>
        )}
        {health.status === "error" && (
          <p className="health health--error" role="alert">
            Unable to connect to the Gibson server.
          </p>
        )}
      </section>
    </main>
  );
}
