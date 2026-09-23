package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/javded-itres/open-comfy/internal/catalog"
	"github.com/javded-itres/open-comfy/internal/importwf"
)

func (s *Server) importPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(importHTML))
}

func (s *Server) analyzeComfyWorkflow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string          `json:"name"`
		Workflow json.RawMessage `json:"workflow"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	allow := importwf.NormalizeAllowlist(s.Cfg.ComfyUI.NodeAllowlist)
	if len(req.Workflow) > 0 {
		info, err := s.Comfy.ObjectInfo(ctx)
		if err != nil {
			writeError(w, 502, "api_error", "comfy_unavailable", err.Error(), "")
			return
		}
		a := importwf.Analyze(req.Workflow, info, allow, s.Cfg.ComfyUI.ModelsDir)
		if req.Name != "" {
			if n, err := importwf.SafeWorkflowName(req.Name); err == nil {
				a.Name = n
			}
		}
		writeJSON(w, 200, a)
		return
	}
	if req.Name == "" {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "name or workflow required", "name")
		return
	}
	a, err := importwf.AnalyzeNamed(ctx, s.Comfy, req.Name, allow, s.Cfg.ComfyUI.ModelsDir)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "name")
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) provisionComfyWorkflow(w http.ResponseWriter, r *http.Request) {
	var req importwf.ProvisionRequest
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.Workflow) == 0 && req.Name == "" {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "name or workflow required", "name")
		return
	}
	if len(req.Workflow) > 0 {
		req.Save = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	token := ""
	if env := s.Cfg.ComfyUI.HFTokenEnv; env != "" {
		token = os.Getenv(env)
	}
	if token == "" {
		token = os.Getenv("HF_TOKEN")
	}
	res, err := importwf.Provision(ctx, s.Comfy, req, importwf.ProvisionConfig{
		Allowlist:      importwf.NormalizeAllowlist(s.Cfg.ComfyUI.NodeAllowlist),
		CustomNodesDir: s.Cfg.ComfyUI.CustomNodesDir,
		ModelsDir:      s.Cfg.ComfyUI.ModelsDir,
		HFToken:        token,
		HFAllow:        s.Cfg.ComfyUI.HFAllowlist,
		ModelMap:       s.Cfg.ComfyUI.ModelMap,
		Downloads:      s.DL,
	})
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "")
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) comfyDownloadStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j := s.DL.Get(id)
	if j == nil {
		writeError(w, 404, "invalid_request_error", "not_found", "download job not found", "id")
		return
	}
	snap := j.Snapshot()
	writeJSON(w, 200, snap)
}

func (s *Server) listComfyWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	items, err := importwf.List(ctx, s.Comfy, s.Cfg.ModelsFile, s.Cfg.WorkflowsDir)
	if err != nil {
		writeError(w, 502, "api_error", "comfy_unavailable", err.Error(), "")
		return
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": items})
}

func (s *Server) importComfyWorkflows(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.Names) == 0 {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "select at least one workflow", "names")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := importwf.ImportSelected(ctx, s.Comfy, s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, req.Names, false)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "names")
		return
	}
	if len(res.Imported) > 0 {
		if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
			s.Cat = cat
		}
	}
	status := 200
	if len(res.Imported) == 0 && len(res.Failed) > 0 {
		status = 400
	}
	writeJSON(w, status, res)
}

func (s *Server) unloadComfyModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(s.maxBody(r)).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", "invalid json", "")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, 400, "invalid_request_error", "invalid_prompt", "select at least one model", "ids")
		return
	}
	res, err := importwf.Unload(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, req.IDs)
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "ids")
		return
	}
	if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
		s.Cat = cat
	}
	status := 200
	if len(res.Removed) == 0 && len(res.Failed) > 0 {
		status = 400
	}
	writeJSON(w, status, res)
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := importwf.Unload(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, []string{id})
	if err != nil {
		writeError(w, 400, "invalid_request_error", "invalid_value", err.Error(), "id")
		return
	}
	if len(res.Removed) == 0 {
		writeError(w, 404, "invalid_request_error", "model_not_found", "model not in OpenComfy", "id")
		return
	}
	if cat, err := catalog.Load(s.Cfg.ModelsFile, s.Cfg.WorkflowsDir, s.Cfg.SkipWorkflow); err == nil {
		s.Cat = cat
	}
	writeJSON(w, 200, res)
}

const importHTML = `<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1"/>
  <title>OpenComfy — import workflows</title>
  <style>
    html { color-scheme: dark; }
    body { font: 15px/1.45 system-ui, sans-serif; margin: 0; background: #0f0f0f; color: #d4d4d4; }
    header { background: #1a1a1a; border-bottom: 1px solid #2a2a2a; padding: 1rem 1.5rem; display: flex; gap: 1rem; align-items: center; flex-wrap: wrap; }
    h1 { font-size: 1.15rem; margin: 0; color: #ffffff; }
    main { max-width: 860px; margin: 1.25rem auto; padding: 0 1rem 3rem; }
    .card { background: #1a1a1a; border: 1px solid #2a2a2a; border-radius: 10px; padding: 1rem 1.1rem; }
    label.row { display: flex; gap: .7rem; align-items: flex-start; padding: .65rem 0; border-bottom: 1px solid #262626; }
    label.row:last-child { border-bottom: 0; }
    label.row.bad { opacity: .85; }
    label.row.err { color: #f87171; }
    .meta { color: #8a8a8a; font-size: 13px; }
    .err { color: #f87171; font-size: 13px; margin-top: .2rem; }
    .ok { color: #4caf50; }
    input[type=password], input[type=text] { padding: .4rem .5rem; background: #242424; border: 1px solid #2a2a2a; color: #d4d4d4; border-radius: 6px; min-width: 18rem; }
    input[type=password]:focus, input[type=text]:focus { border-color: #f04683; outline: none; box-shadow: 0 0 0 2px rgba(240, 70, 131, 0.25); }
    input::placeholder { color: #6b6b6b; }
    button { background: #f04683; color: #fff; border: 0; border-radius: 6px; padding: .45rem .9rem; cursor: pointer; }
    button:hover { background: #ff5a95; }
    button:disabled { background: #4a4a4a; color: #7a7a7a; cursor: not-allowed; }
    button.ghost { background: #242424; color: #f04683; border: 1px solid #2a2a2a; }
    button.ghost:hover { border-color: #f04683; }
    button.danger { background: #b42318; }
    button.danger:hover { background: #d3272a; }
    .bar { display: flex; gap: .6rem; align-items: center; flex-wrap: wrap; margin: 1rem 0; }
    a { color: #f04683; text-decoration: none; }
    a:hover { text-decoration: underline; }
    h2 { font-size: 1rem; margin: 0 0 .5rem; color: #ffffff; }
  </style>
</head>
<body>
  <header>
    <h1>Import ComfyUI workflows</h1>
    <a href="/docs">Swagger</a>
  </header>
  <main>
    <div class="card">
      <div class="bar">
        <input id="key" type="password" placeholder="sk-… API key" autocomplete="off"/>
        <button class="ghost" id="reload" type="button">Обновить</button>
      </div>
      <div id="status" class="meta"></div>
    </div>
    <div class="card" style="margin-top:1rem">
      <h2>Загрузить JSON в ComfyUI</h2>
      <p class="meta">Вариант C: файл сохраняется в библиотеку ComfyUI. Сначала анализ нод и весов. Custom nodes — только allowlist (git). Веса: сначала поиск на диске в <code>comfyui.models_dir</code> по имени файла (symlink в нужную папку), затем Hugging Face. После reboot нод — импорт ниже.</p>
      <div class="bar">
        <input id="wf-file" type="file" accept="application/json,.json"/>
        <input id="wf-name" type="text" placeholder="имя.json"/>
        <label class="meta"><input id="wf-nodes" type="checkbox"/> ноды из allowlist</label>
        <label class="meta"><input id="wf-hf" type="checkbox"/> веса с Hugging Face</label>
        <button class="ghost" type="button" id="wf-analyze">Разобрать</button>
        <button type="button" id="wf-save">Сохранить в ComfyUI</button>
      </div>
      <pre id="wf-report" class="meta" style="white-space:pre-wrap;margin:.6rem 0 0"></pre>
    </div>
    <div class="card" style="margin-top:1rem">
      <h2>В OpenComfy (API)</h2>
      <p class="meta">Убрать модель из <code>GET /v1/models</code>. Файлы в ComfyUI <code>user/default/workflows</code> не трогаем.</p>
      <form id="unload-form">
        <div id="api-list"></div>
        <div class="bar">
          <button class="danger" type="submit" id="drop" disabled>Убрать выбранные из API</button>
          <span id="picked-api" class="meta"></span>
        </div>
      </form>
    </div>
    <div class="card" style="margin-top:1rem">
      <h2>Библиотека ComfyUI</h2>
      <p class="meta">Можно отметить несколько. Без prompt или без reference (LoadImage/LoadVideo) строка заблокирована. Уже импортированные — в списке выше.</p>
      <form id="form">
        <div id="list"></div>
        <div class="bar">
          <button type="submit" id="go" disabled>Загрузить выбранные</button>
          <span id="picked" class="meta"></span>
        </div>
      </form>
    </div>
  </main>
  <script>
    const keyEl = document.getElementById("key");
    const listEl = document.getElementById("list");
    const apiList = document.getElementById("api-list");
    const st = document.getElementById("status");
    const go = document.getElementById("go");
    const drop = document.getElementById("drop");
    const picked = document.getElementById("picked");
    const pickedApi = document.getElementById("picked-api");
    keyEl.value = sessionStorage.getItem("opencomfy_key") || "";
    function key() { return keyEl.value.trim(); }
    function headers() {
      const h = { "Content-Type": "application/json" };
      if (key()) h["Authorization"] = "Bearer " + key();
      return h;
    }
    function syncApi() {
      const n = [...apiList.querySelectorAll("input:checked")].length;
      drop.disabled = n === 0;
      pickedApi.textContent = n ? ("выбрано " + n) : "";
    }
    async function loadApi() {
      apiList.innerHTML = "";
      drop.disabled = true;
      const r = await fetch("/v1/models", { headers: headers() });
      const j = await r.json();
      if (!r.ok) return;
      (j.data || []).forEach(m => {
        const lab = document.createElement("label");
        lab.className = "row";
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.value = m.id;
        cb.addEventListener("change", syncApi);
        const box = document.createElement("div");
        box.innerHTML = "<strong>" + (m.name || m.id) + "</strong> <span class='meta'>" + m.id + "</span>";
        lab.appendChild(cb);
        lab.appendChild(box);
        apiList.appendChild(lab);
      });
      if (!apiList.children.length) {
        apiList.innerHTML = "<p class='meta'>В API пока нет моделей.</p>";
      }
      syncApi();
    }
    async function load() {
      sessionStorage.setItem("opencomfy_key", key());
      st.textContent = "Loading…";
      listEl.innerHTML = "";
      go.disabled = true;
      await loadApi();
      const r = await fetch("/v1/comfy/workflows", { headers: headers() });
      const j = await r.json();
      if (!r.ok) {
        st.textContent = (j.error && j.error.message) || r.statusText;
        return;
      }
      const rows = j.data || [];
      st.textContent = rows.length ? rows.length + " workflows" : "empty library";
      rows.forEach(it => {
        const lab = document.createElement("label");
        lab.className = "row" + (it.can_import ? "" : " bad");
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.value = it.name;
        cb.disabled = !it.can_import;
        cb.addEventListener("change", sync);
        const box = document.createElement("div");
        const title = document.createElement("div");
        title.innerHTML = "<strong>" + (it.title || it.name) + "</strong> <span class='meta'>" + (it.modality || "") + " · " + it.id + "</span>";
        const meta = document.createElement("div");
        meta.className = "meta";
        meta.textContent = (it.params && it.params.length) ? ("params: " + it.params.join(", ")) : "params: —";
        box.appendChild(title);
        box.appendChild(meta);
        (it.errors || []).forEach(e => {
          const d = document.createElement("div");
          d.className = "err";
          d.textContent = e;
          box.appendChild(d);
        });
        lab.appendChild(cb);
        lab.appendChild(box);
        listEl.appendChild(lab);
      });
      sync();
    }
    function sync() {
      const n = [...listEl.querySelectorAll("input:checked")].length;
      go.disabled = n === 0;
      picked.textContent = n ? ("выбрано " + n) : "";
    }
    document.getElementById("reload").onclick = load;
    document.getElementById("form").onsubmit = async (ev) => {
      ev.preventDefault();
      const names = [...listEl.querySelectorAll("input:checked")].map(x => x.value);
      if (!names.length) return;
      go.disabled = true;
      st.textContent = "Importing…";
      const r = await fetch("/v1/comfy/import", {
        method: "POST", headers: headers(),
        body: JSON.stringify({ names })
      });
      const j = await r.json();
      const lines = [];
      (j.imported || []).forEach(s => lines.push("imported: " + s));
      (j.skipped || []).forEach(s => lines.push("skipped: " + s));
      (j.failed || []).forEach(s => lines.push("failed: " + s));
      if (j.error) lines.push(j.error.message || JSON.stringify(j.error));
      st.innerHTML = lines.map(l => "<div>" + l + "</div>").join("") || r.statusText;
      await load();
    };
    document.getElementById("unload-form").onsubmit = async (ev) => {
      ev.preventDefault();
      const ids = [...apiList.querySelectorAll("input:checked")].map(x => x.value);
      if (!ids.length) return;
      if (!confirm("Убрать из OpenComfy (ComfyUI не трогаем): " + ids.join(", ") + "?")) return;
      drop.disabled = true;
      st.textContent = "Removing…";
      const r = await fetch("/v1/comfy/unload", {
        method: "POST", headers: headers(),
        body: JSON.stringify({ ids })
      });
      const j = await r.json();
      const lines = [];
      (j.removed || []).forEach(s => lines.push("removed: " + s));
      (j.failed || []).forEach(s => lines.push("failed: " + s));
      if (j.error) lines.push(j.error.message || JSON.stringify(j.error));
      st.innerHTML = lines.map(l => "<div>" + l + "</div>").join("") || r.statusText;
      await load();
    };
    if (key()) load();
    let uploaded = null;
    const report = document.getElementById("wf-report");
    document.getElementById("wf-file").onchange = async (ev) => {
      const f = ev.target.files && ev.target.files[0];
      if (!f) return;
      document.getElementById("wf-name").value = f.name;
      uploaded = JSON.parse(await f.text());
      report.textContent = "файл: " + f.name + " (" + Math.round(f.size/1024) + " КБ)";
    };
    function wfBody(extra) {
      const name = document.getElementById("wf-name").value.trim();
      const body = Object.assign({ name, overwrite: true }, extra || {});
      if (uploaded) body.workflow = uploaded;
      return body;
    }
    function showAnalysis(j) {
      const lines = [];
      if (j.saved) lines.push("сохранено: " + (j.path || j.name));
      if (j.needs_reboot) lines.push("нужен restart ComfyUI, затем «Обновить»");
      (j.installed || []).forEach(s => lines.push("git: " + s));
      (j.skipped_git || []).forEach(s => lines.push("git skip: " + s));
      (j.missing_nodes || []).forEach(n => lines.push("нода " + n.class_type + (n.git ? " ← " + n.git : "") + (n.allowlisted ? " [allowlist]" : " [не в allowlist]")));
      (j.reused_models || []).forEach(m => lines.push("диск " + m.field + " = " + m.value + (m.local ? " ← " + m.local : "")));
      (j.missing_models || []).forEach(m => lines.push("вес " + m.field + " = " + m.value + " (" + m.class_type + ")"));
      if (j.download_id) lines.push("hf job: " + j.download_id);
      if (j.ready) lines.push("готово к импорту в API (список ниже)");
      else if (!(j.missing_nodes || []).length && !(j.missing_models || []).length) lines.push("анализ пуст");
      (j.errors || []).forEach(e => lines.push(e));
      if (j.error) lines.push(j.error.message || JSON.stringify(j.error));
      report.textContent = lines.join("\n") || JSON.stringify(j, null, 2);
    }
    document.getElementById("wf-analyze").onclick = async () => {
      if (!uploaded && !document.getElementById("wf-name").value.trim()) return;
      report.textContent = "анализ…";
      const r = await fetch("/v1/comfy/analyze", { method: "POST", headers: headers(), body: JSON.stringify(wfBody()) });
      showAnalysis(await r.json());
    };
    document.getElementById("wf-save").onclick = async () => {
      if (!uploaded && !document.getElementById("wf-name").value.trim()) return;
      report.textContent = "сохранение…";
      const r = await fetch("/v1/comfy/provision", {
        method: "POST", headers: headers(),
        body: JSON.stringify(Object.assign(wfBody(), {
          save: true,
          install_nodes: document.getElementById("wf-nodes").checked,
          install_models: document.getElementById("wf-hf").checked
        }))
      });
      const j = await r.json();
      showAnalysis(j);
      if (j.download_id) pollDownload(j.download_id);
      await load();
    };
    async function pollDownload(id) {
      for (let i = 0; i < 3600; i++) {
        const r = await fetch("/v1/comfy/downloads/" + encodeURIComponent(id), { headers: headers() });
        const j = await r.json();
        const lines = ["hf " + (j.status || "")];
        (j.items || []).forEach(it => {
          lines.push((it.status || "") + " " + (it.value || "") + (it.local ? " ← disk " + it.local : (it.repo ? " ← " + it.repo : "")) + (it.error ? " :: " + it.error : "") + (it.bytes ? " " + it.bytes + " B" : ""));
        });
        if (j.error) lines.push(j.error);
        report.textContent = lines.join("\n");
        if (j.status === "completed" || j.status === "failed") {
          await load();
          return;
        }
        await new Promise(res => setTimeout(res, 2000));
      }
    };
  </script>
</body>
</html>
`
