import http from "node:http";
const [port, path, trials] = [Number(process.argv[2]), process.argv[3], Number(process.argv[4])];
const delays = process.argv.slice(5).map(Number);
let n = 0;
const post = (agent) => new Promise((resolve) => {
  const body = JSON.stringify({ email: `g03-${Date.now()}-${n++}@example.test`, display_name: "G03 Probe", mfa_enabled: false, email_verified: true, phone_verified: false });
  const req = http.request({ host: "127.0.0.1", port, method: "POST", path, agent, headers: { "content-type": "application/json" } }, (res) => { res.resume(); res.on("end", () => resolve({ ok: true, reused: req.reusedSocket })); });
  req.on("error", (e) => resolve({ ok: false, reused: req.reusedSocket, err: `${e.code ?? ""} ${e.message}` }));
  req.end(body);
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
for (const d of delays) {
  const res = await Promise.all(Array.from({ length: trials }, async () => {
    const agent = new http.Agent({ keepAlive: true });
    await post(agent); await sleep(d); const b = await post(agent); agent.destroy(); return b;
  }));
  const fail = res.filter((r) => !r.ok), reused = res.filter((r) => r.reused).length;
  console.log(`${d}ms trials=${trials} reused=${reused} failures=${fail.length}${fail.length ? " e.g. " + fail[0].err + " reused=" + fail[0].reused : ""}`);
}
