// Plain Node: server with the default keepAliveTimeout (5000 ms) vs a keep-alive client agent (as Playwright 1.63 uses).
import http from "node:http";
const srv = http.createServer((req, res) => { req.resume(); req.on("end", () => { res.setHeader("content-type", "application/json"); res.end('{"ok":true}'); }); });
await new Promise((r) => srv.listen(0, "127.0.0.1", r));
const port = srv.address().port;
console.log("server keepAliveTimeout", srv.keepAliveTimeout, "node", process.version);
const post = (agent) => new Promise((resolve) => {
  const req = http.request({ host: "127.0.0.1", port, method: "POST", path: "/x", agent, headers: { "content-type": "application/json" } }, (res) => {
    res.resume(); res.on("end", () => resolve({ ok: true, reused: req.reusedSocket, ka: res.headers["keep-alive"] }));
  });
  req.on("error", (e) => resolve({ ok: false, reused: req.reusedSocket, err: `${e.code ?? ""} ${e.message}` }));
  req.end('{"a":1}');
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const delays = process.argv.slice(2).map(Number);
const tally = {};
for (const d of delays) for (let t = 0; t < 10; t++) {
  const agent = new http.Agent({ keepAlive: true });
  const a = await post(agent); await sleep(d); const b = await post(agent); agent.destroy();
  const k = `${d}ms ${b.ok ? "ok" : "FAIL:" + b.err} reused=${b.reused}`; tally[k] = (tally[k] ?? 0) + 1;
  if (t === 0 && d === delays[0]) console.log("first response keep-alive header:", a.ka);
}
for (const [k, v] of Object.entries(tally)) console.log(v, k);
srv.close();
