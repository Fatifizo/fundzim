import http from "node:http";
const post = (port, agent) => new Promise((res) => { const r = http.request({ host: "127.0.0.1", port, method: "POST", path: "/", agent }, (x) => { x.resume(); x.on("end", () => res(r.reusedSocket)); }); r.on("error", (e) => res("ERR " + e.code)); r.end("{}"); });
for (const [kat, buf] of [[1000, 0], [2000, 0], [3000, 0], [5000, 1000]]) {
  const srv = http.createServer((q, s) => { q.resume(); q.on("end", () => s.end("{}")); }); srv.keepAliveTimeout = kat; srv.keepAliveTimeoutBuffer = buf;
  await new Promise((r) => srv.listen(0, "127.0.0.1", r)); const port = srv.address().port;
  const agent = new http.Agent({ keepAlive: true });
  const a = await post(port, agent); const b = await post(port, agent); await new Promise((r) => setTimeout(r, 50)); const c = await post(port, agent);
  console.log(`server keepAliveTimeout=${kat} buffer=${buf}: back-to-back reused=${b}, after 50ms reused=${c}`);
  agent.destroy(); srv.close();
}
console.log("agent option agentKeepAliveTimeoutBuffer default:", new http.Agent({ keepAlive: true }).agentKeepAliveTimeoutBuffer ?? "n/a (not present)");
