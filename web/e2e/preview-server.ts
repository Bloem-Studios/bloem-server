import http from "node:http";
import { previewFixture, type PreviewFixtureState } from "./preview-fixtures.ts";
const state: PreviewFixtureState = { exchanges: 0, unexpected: [] };
http
  .createServer((req, res) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    if (url.pathname === "/_fixture/requests") {
      res.setHeader("Content-Type", "application/json");
      res.end(JSON.stringify(state));
      return;
    }
    const reply = previewFixture(req.method ?? "GET", decodeURIComponent(url.pathname), state);
    res.writeHead(reply.status, {
      "Content-Type": reply.status >= 400 ? "application/problem+json" : "application/json",
    });
    res.end(reply.body === undefined ? undefined : JSON.stringify(reply.body));
  })
  .listen(4186, "127.0.0.1", () => process.stdout.write("Synthetic preview API on 4186\n"));
