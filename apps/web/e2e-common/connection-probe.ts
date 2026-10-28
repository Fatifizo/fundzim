import http from "node:http";
import type { AddressInfo, Socket } from "node:net";

import type { APIRequestContext } from "@playwright/test";

// G-03 regression probe shared by e2e/ and e2e-real/ request-connections specs. The probe server lives in the test
// process with a 2 s idle timeout and no grace buffer, so its close lines up with a request sent exactly then. (At 1 s
// or less the advertised `Keep-Alive: timeout=1` falls within Node's client-side buffer and connections are not reused
// at all, which would hide the condition.)

export const IDLE_CLOSE_MS = 2000;

export async function probeServer() {
  const seen: { socket: Socket; connection: string | undefined; path: string }[] = [];
  const server = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      seen.push({ socket: req.socket, connection: req.headers.connection, path: req.url ?? "" });
      if (req.url === "/page") {
        res.setHeader("content-type", "text/html");
        res.end("<!doctype html><title>probe</title><p>probe</p>");
        return;
      }
      res.statusCode = req.url === "/fail" ? 500 : 201;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ ok: res.statusCode === 201 }));
    });
  });
  server.keepAliveTimeout = IDLE_CLOSE_MS;
  server.keepAliveTimeoutBuffer = 0;
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return { url, seen, close: () => new Promise<void>((resolve) => server.close(() => resolve())) };
}

export const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Sends `rounds` POSTs, each exactly when the server closes the previous request's idle connection. */
export async function postAcrossIdleClose(api: APIRequestContext, url: string, rounds: number) {
  const outcomes: string[] = [];
  for (let i = 0; i < rounds; i++) {
    if (i > 0) await pause(IDLE_CLOSE_MS);
    outcomes.push(await api.post(`${url}/setup`, { data: { i } }).then((r) => String(r.status()), (e: Error) => e.message.split("\n")[0] ?? e.message));
  }
  return outcomes;
}

