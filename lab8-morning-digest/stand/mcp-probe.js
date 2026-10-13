const URL = process.argv[2];
const TOKEN = process.argv[3] || "";

async function rpc(body, session) {
  const h = {
    "Content-Type": "application/json",
    "Accept": "application/json, text/event-stream",
  };
  if (TOKEN) h["Authorization"] = "Bearer " + TOKEN;
  if (session) h["mcp-session-id"] = session;
  const r = await fetch(URL, { method: "POST", headers: h, body: JSON.stringify(body) });
  const sid = r.headers.get("mcp-session-id");
  const text = await r.text();
  let json = null;
  for (const line of text.split("\n")) {
    const s = line.startsWith("data: ") ? line.slice(6) : line;
    if (!s.trim()) continue;
    try { json = JSON.parse(s); } catch (e) {}
  }
  return { sid, json, status: r.status };
}

(async () => {
  const init = await rpc({
    jsonrpc: "2.0", id: 1, method: "initialize",
    params: { protocolVersion: "2024-11-05", capabilities: {},
              clientInfo: { name: "probe", version: "1" } },
  });
  console.log("initialize: HTTP " + init.status + ", сессия " + (init.sid || "нет"));
  if (init.json && init.json.error) { console.log(JSON.stringify(init.json.error)); return; }
  await rpc({ jsonrpc: "2.0", method: "notifications/initialized" }, init.sid);
  const list = await rpc({ jsonrpc: "2.0", id: 2, method: "tools/list" }, init.sid);
  const tools = (list.json && list.json.result && list.json.result.tools) || [];
  console.log("инструментов: " + tools.length);
  for (const t of tools) {
    const req = ((t.inputSchema || {}).required || []).join(", ");
    console.log("  " + t.name.padEnd(26) + " обязательные: " + (req || "-"));
  }
})();
