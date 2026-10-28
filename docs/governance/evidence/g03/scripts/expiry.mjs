import http from "node:http";
const post = (port, agent) => new Promise((res) => { const r = http.request({ host: "127.0.0.1", port, method: "POST", path: "/", agent }, (x) => { x.resume(); x.on("end", () => res(r.reusedSocket ? "reused" : "new")); }); r.on("error", (e) => res("ERR " + e.code + " reused=" + r.reusedSocket)); r.end("{}"); });
const [kat, buf] = process.argv.slice(2, 4).map(Number); const delays = process.argv.slice(4).map(Number);
const srv = http.createServer((q, s) => { q.resume(); q.on("end", () => s.end("{}")); }); srv.keepAliveTimeout = kat; srv.keepAliveTimeoutBuffer = buf;
await new Promise((r) => srv.listen(0, "127.0.0.1", r)); const port = srv.address().port;
for (const d of delays) { const t = {}; for (let i = 0; i < 6; i++) { const agent = new http.Agent({ keepAlive: true }); await post(port, agent); await new Promise((r) => setTimeout(r, d)); const o = await post(port, agent); t[o] = (t[o] ?? 0) + 1; agent.destroy(); } console.log(`server ${kat}+${buf}ms, gap ${d}ms:`, JSON.stringify(t)); }
srv.close();
