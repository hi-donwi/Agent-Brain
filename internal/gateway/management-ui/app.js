const $ = (selector) => document.querySelector(selector);
const state = {
  providers: [], accounts: [], profiles: [], pending: false, loginSession: null,
  runtime: null, installedModels: [], modelCatalog: [], modelProfiles: [], modelBackendID: '',
  modelJobs: [], currentModelJob: null, modelPolling: false, runtimeError: '',
  knowledgeSources: [], knowledgeJobs: [], currentKnowledgeJob: null, knowledgePolling: false,
  knowledgeResults: [], knowledgeError: '',
  brainSkills: [], mcpInfo: null, currentSkill: null, skillsError: '',
  traceSessions: [], evaluationSuites: [], evaluationJobs: [], currentEvaluationJob: null,
  evaluationPolling: false, tracesError: '',
  forgeCandidates: [], forgeError: '',
  serviceStatus: null, serviceError: '',
};
const tokenInput = $('#management-token');

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>"']/g, (char) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  })[char]);
}

function status(message) { $('#status').textContent = message; }
function showError(error) {
  $('#error').textContent = error instanceof Error ? error.message : 'The request failed.';
  $('#error').hidden = false;
}
function clearError() { $('#error').textContent = ''; $('#error').hidden = true; }
function setPending(pending) {
  state.pending = pending;
  $('#workspace').setAttribute('aria-busy', String(pending));
  $('#connect-form button[type="submit"]').disabled = pending;
  $('#disconnect').disabled = pending;
  $('#workspace').querySelectorAll('button').forEach((button) => {
    button.disabled = pending || button.dataset.disabled === 'true';
  });
}
function managementToken() {
  const value = tokenInput.value.trim();
  if (!value) throw new Error('Enter the management credential issued by Brain.');
  return value;
}

async function request(path, options = {}) {
  const response = await fetch(`/manage/v1${path}`, {
    method: options.method ?? 'GET',
    headers: {
      Authorization: `Bearer ${managementToken()}`,
      ...(options.body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    ...(options.body === undefined ? {} : { body: JSON.stringify(options.body) }),
    cache: 'no-store',
    credentials: 'omit',
    redirect: 'error',
  });
  if (!response.ok) {
    let message = `Brain rejected the request (HTTP ${response.status}).`;
    try { message = (await response.json()).error?.message ?? message; } catch { /* retain status message */ }
    throw new Error(message);
  }
  return response.json();
}

function renderProviders() {
  $('#providers').innerHTML = state.providers.map((provider) => `
    <li>
      <strong>${escapeHTML(provider.name)}</strong>
      <span>${escapeHTML((provider.auth ?? []).join(', ') || 'CLI configuration')} · ${escapeHTML((provider.protocols ?? []).join(', ') || 'no inference protocol')}</span>
      ${provider.key_url && /^https:\/\//.test(provider.key_url) ? `<a href="${escapeHTML(provider.key_url)}" target="_blank" rel="noopener noreferrer">Provider key settings</a>` : ''}
      ${provider.note ? `<small>${escapeHTML(provider.note)}</small>` : ''}
    </li>`).join('');

  const select = $('#provider');
  const current = select.value;
  const manageable = state.providers.filter((provider) => (provider.auth ?? []).some((auth) => ['api-key', 'none', 'oauth', 'native'].includes(auth)));
  select.innerHTML = manageable.map((provider) => `<option value="${escapeHTML(provider.id)}">${escapeHTML(provider.name)}</option>`).join('');
  if (manageable.some((provider) => provider.id === current)) select.value = current;
  else if (manageable.length) select.value = manageable[0].id;
  updateProviderFields();
}

function updateProviderFields() {
  const provider = state.providers.find((item) => item.id === $('#provider').value);
  const auth = $('#auth');
  const supported = (provider?.auth ?? []).filter((item) => ['api-key', 'none', 'oauth', 'native'].includes(item));
  auth.innerHTML = supported.map((item) => `<option value="${escapeHTML(item)}">${escapeHTML(item)}</option>`).join('');
  auth.value = supported.includes('api-key') ? 'api-key' : (supported[0] ?? 'api-key');
  $('#credential').disabled = auth.value !== 'api-key';
  $('#credential').required = false;
  $('#base-url').value = provider?.base_url ?? '';
}

function renderAccounts() {
  if (state.accounts.length === 0) {
    $('#accounts').innerHTML = '<li>No provider accounts are configured.</li>';
    return;
  }
  $('#accounts').innerHTML = state.accounts.map((account) => `
    <li>
      <strong>${escapeHTML(account.provider)}/${escapeHTML(account.name)}</strong>
      <span>${escapeHTML(account.status)} · ${escapeHTML(account.auth)} · ${account.enabled ? 'enabled' : 'disabled'} · ${account.credential_set ? 'credential configured' : 'no credential'}</span>
      <small>${escapeHTML([...(account.models ?? []), ...(account.discovered_models ?? [])].join(', ') || 'No model IDs recorded')}</small>
      <div class="account-actions">
        ${account.auth === 'oauth' && !account.credential_set ? `<button type="button" data-action="login" data-id="${escapeHTML(account.id)}">${account.status === 'reauth' ? 'Reconnect' : 'Start sign-in'}</button>` : ''}
        ${account.auth !== 'native' ? `<button type="button" data-action="test" data-id="${escapeHTML(account.id)}">Test without inference</button>` : ''}
        ${account.auth !== 'native' ? `<button type="button" data-action="refresh" data-id="${escapeHTML(account.id)}">Refresh models</button>` : ''}
        <button type="button" data-action="toggle" data-id="${escapeHTML(account.id)}">${account.enabled ? 'Disable' : 'Enable'}</button>
        <button type="button" data-action="default" data-id="${escapeHTML(account.id)}" data-disabled="${!account.enabled}" ${account.enabled ? '' : 'disabled'}>Set default</button>
        <button type="button" data-action="remove" data-id="${escapeHTML(account.id)}">Remove binding</button>
      </div>
      ${account.auth === 'native' ? `<small>Native sign-in runs in the official local client. In a Brain terminal run <code>brain accounts login ${escapeHTML(account.provider)}/${escapeHTML(account.name)}</code>.</small>` : ''}
    </li>`).join('');
}

function renderProfiles() {
  $('#profiles').innerHTML = state.profiles.map((entry) => `
    <li>
      <label for="profile-${escapeHTML(entry.profile.id)}">${escapeHTML(entry.profile.id)} · ${escapeHTML(entry.revision)}</label>
      <textarea id="profile-${escapeHTML(entry.profile.id)}" spellcheck="false">${escapeHTML(JSON.stringify(entry.profile, null, 2))}</textarea>
      <button type="button" class="primary" data-action="save-profile" data-id="${escapeHTML(entry.profile.id)}">Validate and save profile</button>
    </li>`).join('') || '<li>No routing profiles are configured.</li>';
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function renderRuntime() {
  const runtime = state.runtime;
  const summary = $('#runtime-summary');
  const backendSelect = $('#backend-select');
  const profileSelect = $('#hardware-profile');
  if (!runtime) {
    summary.textContent = state.runtimeError || 'Brain runtime status is unavailable.';
    backendSelect.innerHTML = '';
    profileSelect.innerHTML = '';
    $('#select-backend').dataset.disabled = 'true';
    $('#select-backend').disabled = true;
    $('#select-profile').dataset.disabled = 'true';
    $('#select-profile').disabled = true;
    $('#installed-models').innerHTML = '<li>Installed models are unavailable.</li>';
    $('#available-models').innerHTML = '<li>Model catalog is unavailable.</li>';
    renderModelJob(state.currentModelJob);
    return;
  }
  const gpuNames = (runtime.hardware?.gpus ?? []).map((gpu) => gpu.name).filter(Boolean).join(', ');
  summary.innerHTML = [
    `<span>${escapeHTML(runtime.hardware?.os)} · ${escapeHTML(runtime.hardware?.arch)}</span>`,
    `<span>${escapeHTML(runtime.hardware?.cpu || 'CPU')} · ${runtime.hardware?.logical_cpus ?? 0} threads</span>`,
    `<span>${escapeHTML(runtime.hardware?.accelerator || 'accelerator unknown')}${gpuNames ? ` · ${escapeHTML(gpuNames)}` : ''}</span>`,
    `<span>${formatBytes(runtime.hardware?.memory_bytes ?? 0)} memory</span>`,
    `<span>${runtime.free_disk_known ? `${formatBytes(runtime.free_disk_bytes)} free disk` : 'Free disk unavailable'}</span>`,
    `<span>${escapeHTML(runtime.running_backend_id || 'no backend')} ${runtime.backend_reachable ? 'reachable' : 'unreachable'}</span>`,
    `<span>Profile: ${escapeHTML(runtime.profile)}</span>`,
    ...(runtime.restart_required ? ['<span>Restart Brain to apply configured backend</span>'] : []),
  ].join('');
  const currentBackend = backendSelect.value;
  backendSelect.innerHTML = (runtime.backends ?? []).map((item) =>
    `<option value="${escapeHTML(item.id)}">${escapeHTML(item.id)} · ${escapeHTML(item.endpoint)}${item.token_configured ? ' · token set' : ''}</option>`).join('');
  backendSelect.value = (runtime.backends ?? []).some((item) => item.id === currentBackend)
    ? currentBackend : runtime.configured_backend_id;
  const currentProfile = profileSelect.value;
  profileSelect.innerHTML = state.modelProfiles.map((profile) =>
    `<option value="${escapeHTML(profile.name)}">${escapeHTML(profile.name)} · ${escapeHTML(profile.description)}</option>`).join('');
  profileSelect.value = state.modelProfiles.some((profile) => profile.name === currentProfile)
    ? currentProfile : runtime.profile;
  $('#select-backend').dataset.disabled = String(!runtime.backends?.length);
  $('#select-backend').disabled = !runtime.backends?.length;
  $('#select-profile').dataset.disabled = String(!state.modelProfiles.length);
  $('#select-profile').disabled = !state.modelProfiles.length;

  $('#installed-models').innerHTML = state.installedModels.map((model) => {
    const inUse = Object.values(runtime.aliases ?? {}).includes(model.catalog_id) || Object.values(runtime.aliases ?? {}).includes(model.name);
    return `<li>
      <strong>${escapeHTML(model.name)}</strong>
      <small>${formatBytes(model.size)}${model.running ? ' · loaded in memory' : ''}${model.catalog_id ? ` · ${escapeHTML(model.catalog_id)} · ${(model.roles ?? []).map(escapeHTML).join(', ')}` : ''}</small>
      <div class="model-actions">
        ${model.catalog_id ? `<button type="button" data-action="unload-model" data-id="${escapeHTML(model.catalog_id)}" data-disabled="${!model.running || !runtime.model_management_supported}" ${!model.running || !runtime.model_management_supported ? 'disabled' : ''}>Unload</button>` : ''}
        ${model.catalog_id ? `<button type="button" data-action="delete-model" data-id="${escapeHTML(model.catalog_id)}" data-disabled="${inUse || model.running || !runtime.model_management_supported}" ${inUse || model.running || !runtime.model_management_supported ? 'disabled' : ''}>Remove model</button>` : ''}
      </div>
    </li>`;
  }).join('') || '<li>No models are installed on the running backend.</li>';
  const installedCatalogIDs = new Set(state.installedModels.map((model) => model.catalog_id).filter(Boolean));
  const backendForCatalog = state.modelBackendID;
  $('#available-models').innerHTML = state.modelCatalog.map((model) => {
    const installed = installedCatalogIDs.has(model.id);
    const tag = model.backends?.[backendForCatalog] ?? '';
    return `<li>
      <strong>${escapeHTML(model.name)} · ${escapeHTML(model.id)}</strong>
      <small>${escapeHTML((model.roles ?? []).join(', '))} · ${formatBytes(model.download_bytes)} download · ${formatBytes(model.memory_bytes)} memory · ${escapeHTML(model.licence)}</small>
      <small>${escapeHTML(tag ? `Backend tag: ${tag}` : 'Not available on this backend')}</small>
      <div class="model-actions"><button type="button" data-action="pull-model" data-id="${escapeHTML(model.id)}" data-disabled="${installed || !tag || !runtime.model_management_supported || !runtime.backend_reachable}" ${installed || !tag || !runtime.model_management_supported || !runtime.backend_reachable ? 'disabled' : ''}>Download model</button></div>
    </li>`;
  }).join('') || '<li>No catalog models are available for the running backend.</li>';
  renderModelJob(state.currentModelJob);
}

function renderModelJob(job) {
  const panel = $('#model-job-panel');
  if (!job) { panel.hidden = true; return; }
  panel.hidden = false;
  $('#model-job-title').textContent = `Model download: ${job.catalog_model_id}`;
  $('#model-job-status').textContent = `${job.status}${job.error_code ? ` · ${job.error_code}` : ''} · ${formatBytes(job.completed_bytes)} / ${formatBytes(job.total_bytes)}`;
  const progress = $('#model-job-progress');
  progress.value = job.total_bytes > 0 ? Math.min(100, job.completed_bytes / job.total_bytes * 100) : 0;
  const canCancel = ['queued', 'running', 'canceling'].includes(job.status);
  $('#cancel-model-job').hidden = !canCancel;
  $('#cancel-model-job').dataset.disabled = String(!canCancel);
  $('#cancel-model-job').disabled = !canCancel;
}

async function loadRuntimeData() {
  const [runtimeResult, modelsResult, catalogResult, jobsResult] = await Promise.allSettled([
    request('/runtime'), request('/models'), request('/model-catalog'), request('/model-pull-jobs'),
  ]);
  state.runtime = runtimeResult.status === 'fulfilled' ? runtimeResult.value : null;
  state.installedModels = modelsResult.status === 'fulfilled' ? modelsResult.value.models : [];
  state.modelCatalog = catalogResult.status === 'fulfilled' ? catalogResult.value.models : [];
  state.modelProfiles = catalogResult.status === 'fulfilled' ? catalogResult.value.profiles : [];
  state.modelBackendID = catalogResult.status === 'fulfilled' ? catalogResult.value.backend_id : '';
  state.modelJobs = jobsResult.status === 'fulfilled' ? jobsResult.value.jobs : [];
  state.runtimeError = runtimeResult.status === 'rejected' ? runtimeResult.reason.message : '';
  const activeJob = state.modelJobs.find((job) => ['queued', 'running', 'canceling'].includes(job.status));
  state.currentModelJob = activeJob ?? state.modelJobs[0] ?? null;
  renderRuntime();
  if (activeJob && !state.modelPolling) void pollModelJob(activeJob.id);
}

async function loadAll() {
  const [providers, accounts, profiles] = await Promise.all([
    request('/providers'), request('/accounts'), request('/profiles'),
  ]);
  state.providers = providers.providers;
  state.accounts = accounts.accounts;
  state.profiles = profiles.profiles;
  renderProviders();
  renderAccounts();
  renderProfiles();
  $('#workspace').hidden = false;
  $('#disconnect').hidden = false;
  await loadRuntimeData();
  await loadKnowledgeData();
  await loadToolingData();
  await loadTraceEvalData();
  await loadForgeData();
  await loadServiceStatus();
  status(`Connected to Brain. ${state.accounts.length} account(s), ${state.profiles.length} routing profile(s). Changes require a gateway restart.`);
}

async function loadServiceStatus() {
  try {
    state.serviceStatus = await request('/service');
    state.serviceError = '';
  } catch (error) {
    state.serviceStatus = null;
    state.serviceError = error instanceof Error ? error.message : 'Service status is unavailable.';
  }
  renderServiceStatus();
}

function renderServiceStatus() {
  const status = state.serviceStatus;
  $('#service-status').innerHTML = status
    ? [
      `<span>${escapeHTML(status.service_type)} service ${status.installed ? 'installed' : 'not installed'}</span>`,
      `<span>Gateway ${status.running ? 'reachable' : 'not reachable'}</span>`,
      ...(status.version ? [`<span>Version ${escapeHTML(status.version)}</span>`] : []),
      ...(status.profile ? [`<span>Profile ${escapeHTML(status.profile)}</span>`] : []),
    ].join('')
    : `<span>${escapeHTML(state.serviceError || 'Service status is unavailable.')}</span>`;
}

async function loadForgeData() {
  try {
    const response = await request('/forge/candidates');
    state.forgeCandidates = response.candidates;
    state.forgeError = '';
  } catch (error) {
    state.forgeCandidates = [];
    state.forgeError = error instanceof Error ? error.message : 'Forge candidate inventory is unavailable.';
  }
  renderForgeCandidates();
}

function renderForgeCandidates() {
  $('#forge-candidates').innerHTML = state.forgeCandidates.map((candidate) => `
    <li>
      <strong>${escapeHTML(candidate.id)} · ${escapeHTML(candidate.state)}</strong>
      <small>Review ${candidate.reviewed ? 'complete' : 'required'}${candidate.external_content_reviewed ? ` · external content ${candidate.external_content_included ? 'included' : 'not included'}` : ''} · ${candidate.source_session_count} source session(s)</small>
      <small>Structure ${candidate.structural_passed ? 'pass' : 'fail'} · safety ${candidate.safety_passed ? 'pass' : 'fail'} · evaluation ${candidate.evaluation_fresh ? (candidate.evaluation_passed ? 'fresh/pass' : 'fresh/fail') : 'missing or stale'}</small>
      ${candidate.evaluation ? `<small>Precision ${(candidate.evaluation.precision * 100).toFixed(1)}% · recall ${(candidate.evaluation.recall * 100).toFixed(1)}%</small>` : ''}
      <div class="model-actions">
        <button type="button" data-action="forge-validate" data-id="${escapeHTML(candidate.id)}">Validate</button>
        <button type="button" data-action="forge-evaluate" data-id="${escapeHTML(candidate.id)}">Evaluate</button>
        <button type="button" data-action="forge-promote" data-id="${escapeHTML(candidate.id)}" data-disabled="${!candidate.promotion_ready}" ${candidate.promotion_ready ? '' : 'disabled'}>Promote</button>
        <button type="button" data-action="forge-discard" data-id="${escapeHTML(candidate.id)}">Discard</button>
      </div>
    </li>`).join('') || `<li>${escapeHTML(state.forgeError || 'No Forge candidates are stored in Brain.')}</li>`;
}

async function loadToolingData() {
  const [skillsResult, mcpResult] = await Promise.allSettled([
    request('/skills'), request('/mcp'),
  ]);
  state.brainSkills = skillsResult.status === 'fulfilled' ? skillsResult.value.skills : [];
  state.mcpInfo = mcpResult.status === 'fulfilled' ? mcpResult.value : null;
  state.skillsError = skillsResult.status === 'rejected'
    ? (skillsResult.reason instanceof Error ? skillsResult.reason.message : 'Brain skills are unavailable.')
    : '';
  if (state.currentSkill && !state.brainSkills.some((skill) => skill.id === state.currentSkill.id)) state.currentSkill = null;
  renderTooling();
}

function renderTooling() {
  $('#brain-skills').innerHTML = state.brainSkills.map((skill) => `
    <li>
      <strong>${escapeHTML(skill.name)}${skill.valid ? '' : ' · validation needed'}</strong>
      <small>${escapeHTML(skill.description)}${skill.pack ? ` · ${escapeHTML(skill.pack)}` : ''}</small>
      <div class="model-actions"><button type="button" data-action="inspect-skill" data-id="${escapeHTML(skill.id)}">Inspect user skill</button></div>
    </li>`).join('') || `<li>${escapeHTML(state.skillsError || 'No Brain-user skills are installed.')}</li>`;

  const detail = $('#skill-detail');
  if (!state.currentSkill) {
    detail.hidden = true;
  } else {
    detail.hidden = false;
    $('#skill-detail-title').textContent = `${state.currentSkill.name} · ${state.currentSkill.source}`;
    $('#skill-detail-description').textContent = state.currentSkill.description;
    $('#skill-detail-body').textContent = state.currentSkill.body;
    $('#skill-detail-validation').textContent = state.currentSkill.references?.length
      ? `References: ${state.currentSkill.references.join(', ')}` : '';
  }
  $('#mcp-tools').innerHTML = (state.mcpInfo?.tools ?? []).map((tool) => `
    <li><strong>${escapeHTML(tool.name)} · ${tool.read_only ? 'read-only' : 'restricted'}</strong><small>${escapeHTML(tool.description)}</small></li>`).join('')
      || `<li>${escapeHTML(state.skillsError || 'MCP tool inventory is unavailable.')}</li>`;
}

async function loadTraceEvalData() {
  const [tracesResult, suitesResult, jobsResult] = await Promise.allSettled([
    request('/traces'), request('/evaluation-suites'), request('/evaluation-jobs'),
  ]);
  state.traceSessions = tracesResult.status === 'fulfilled' ? tracesResult.value.sessions : [];
  state.evaluationSuites = suitesResult.status === 'fulfilled' ? suitesResult.value.suites : [];
  state.evaluationJobs = jobsResult.status === 'fulfilled' ? jobsResult.value.jobs : [];
  state.tracesError = tracesResult.status === 'rejected'
    ? (tracesResult.reason instanceof Error ? tracesResult.reason.message : 'Trace summaries are unavailable.')
    : '';
  const activeJob = state.evaluationJobs.find((job) => ['queued', 'running', 'canceling'].includes(job.status));
  state.currentEvaluationJob = activeJob ?? state.evaluationJobs[0] ?? null;
  renderTraceEval();
  if (activeJob && !state.evaluationPolling) void pollEvaluationJob(activeJob.id);
}

function renderTraceEval() {
  $('#trace-summaries').innerHTML = state.traceSessions.map((item) => `
    <li>
      <strong>${escapeHTML(item.id)} · ${item.record_count} turn(s)</strong>
      <small>${escapeHTML((item.models ?? []).join(', ') || 'No model metadata')} · ${item.total_prompt_tokens} prompt / ${item.total_eval_tokens} output tokens · ${Math.round(item.total_duration_ms / 1000)}s total · ${item.has_errors ? 'contains errors' : 'no errors'}</small>
      <small>${item.first_timestamp ? new Date(item.first_timestamp).toLocaleString() : 'time unavailable'} – ${item.last_timestamp ? new Date(item.last_timestamp).toLocaleString() : 'time unavailable'}</small>
    </li>`).join('') || `<li>${escapeHTML(state.tracesError || 'No trace summaries are recorded.')}</li>`;

  const modelSelect = $('#evaluation-model');
  const currentModel = modelSelect.value;
  const installed = state.installedModels.filter((model) => model.catalog_id);
  modelSelect.innerHTML = installed.map((model) => `<option value="${escapeHTML(model.catalog_id)}">${escapeHTML(model.name)} · ${escapeHTML(model.catalog_id)}</option>`).join('');
  if (installed.some((model) => model.catalog_id === currentModel)) modelSelect.value = currentModel;
  else if (installed.length) modelSelect.value = installed[0].catalog_id;
  modelSelect.disabled = !installed.length || !state.runtime?.model_management_supported || !state.runtime?.backend_reachable;
  const runButton = $('#local-evaluation button[type="submit"]');
  runButton.dataset.disabled = String(modelSelect.disabled);
  runButton.disabled = modelSelect.disabled;
  renderEvaluationJob(state.currentEvaluationJob);
}

function renderEvaluationJob(job) {
  const panel = $('#evaluation-job-panel');
  if (!job) { panel.hidden = true; return; }
  panel.hidden = false;
  $('#evaluation-job-title').textContent = `Local evaluation · ${job.model_id}`;
  const result = job.status === 'completed' ? (job.passed ? ' · PASS' : ' · FAIL') : '';
  $('#evaluation-job-status').textContent = `${job.status}${result}${job.error_code ? ` · ${job.error_code}` : ''} · ${job.duration_ms ?? 0} ms · ${job.prompt_tokens ?? 0} prompt / ${job.eval_tokens ?? 0} output tokens`;
  $('#evaluation-assertions').innerHTML = (job.assertions ?? []).map((assertion) =>
    `<li>${escapeHTML(assertion.type)} · ${assertion.passed ? 'PASS' : 'FAIL'}</li>`).join('');
  const canCancel = ['queued', 'running', 'canceling'].includes(job.status);
  $('#cancel-evaluation-job').hidden = !canCancel;
  $('#cancel-evaluation-job').dataset.disabled = String(!canCancel);
  $('#cancel-evaluation-job').disabled = !canCancel;
}

async function loadKnowledgeData() {
  const [knowledgeResult, jobsResult] = await Promise.allSettled([
    request('/knowledge'), request('/knowledge/index-jobs'),
  ]);
  state.knowledgeSources = knowledgeResult.status === 'fulfilled' ? knowledgeResult.value.sources : [];
  state.knowledgeJobs = jobsResult.status === 'fulfilled' ? jobsResult.value.jobs : [];
  state.knowledgeError = knowledgeResult.status === 'rejected'
    ? (knowledgeResult.reason instanceof Error ? knowledgeResult.reason.message : 'Knowledge state is unavailable.')
    : '';
  const activeJob = state.knowledgeJobs.find((job) => ['queued', 'running', 'canceling'].includes(job.status));
  state.currentKnowledgeJob = activeJob ?? state.knowledgeJobs[0] ?? null;
  renderKnowledge();
  if (activeJob && !state.knowledgePolling) void pollKnowledgeJob(activeJob.id);
}

function renderKnowledge() {
  const sources = state.knowledgeSources;
  $('#knowledge-sources').innerHTML = sources.map((source) => `
    <li>
      <strong>${escapeHTML(source.name)} · ${escapeHTML(source.id)}</strong>
      <small>Collection ${escapeHTML(source.collection)} · ${source.root_available ? 'registered root available' : 'registered root unavailable'} · ${source.indexed ? `${source.documents} documents · ${source.chunks} chunks · ${formatBytes(source.bytes)}` : 'not indexed yet'}</small>
      <div class="model-actions"><button type="button" data-action="index-source" data-id="${escapeHTML(source.id)}" data-disabled="${!source.root_available || !state.runtime?.backend_reachable || !state.runtime?.model_management_supported}" ${!source.root_available || !state.runtime?.backend_reachable || !state.runtime?.model_management_supported ? 'disabled' : ''}>Reindex</button></div>
    </li>`).join('') || `<li>${escapeHTML(state.knowledgeError || 'No knowledge roots have been registered in Brain yet.')}</li>`;
  const select = $('#knowledge-source-select');
  const selected = select.value;
  select.innerHTML = sources.map((source) => `<option value="${escapeHTML(source.id)}">${escapeHTML(source.name)} · ${escapeHTML(source.collection)}</option>`).join('');
  if (sources.some((source) => source.id === selected)) select.value = selected;
  else if (sources.length) select.value = sources[0].id;
  select.disabled = !sources.length;
  $('#knowledge-search button[type="submit"]').dataset.disabled = String(!sources.length);
  $('#knowledge-search button[type="submit"]').disabled = !sources.length;
  $('#knowledge-results').innerHTML = state.knowledgeResults.map((result) => `
    <li>
      <strong>${escapeHTML(result.path)}:${result.start_line}-${result.end_line} · ${Number(result.score).toFixed(3)} · ${escapeHTML(result.match_type)}</strong>
      <p>${escapeHTML(result.snippet)}</p>
    </li>`).join('');
  renderKnowledgeJob(state.currentKnowledgeJob);
}

function renderKnowledgeJob(job) {
  const panel = $('#knowledge-job-panel');
  if (!job) { panel.hidden = true; return; }
  panel.hidden = false;
  $('#knowledge-job-title').textContent = `Index job: ${job.source_name}`;
  $('#knowledge-job-status').textContent = `${job.status}${job.error_code ? ` · ${job.error_code}` : ''} · ${job.files_indexed} files · ${job.chunks_created} chunks`;
  const canCancel = ['queued', 'running', 'canceling'].includes(job.status);
  $('#cancel-knowledge-job').hidden = !canCancel;
  $('#cancel-knowledge-job').dataset.disabled = String(!canCancel);
  $('#cancel-knowledge-job').disabled = !canCancel;
}

async function run(action, success) {
  if (state.pending) return;
  setPending(true);
  clearError();
  status('Working…');
  try {
    await action();
    status(success);
  } catch (error) {
    showError(error);
    status('The requested change was not completed.');
  } finally {
    setPending(false);
  }
}

$('#connect-form').addEventListener('submit', (event) => {
  event.preventDefault();
  void run(loadAll, 'Brain management is connected.');
});

$('#disconnect').addEventListener('click', () => {
  tokenInput.value = '';
  $('#workspace').hidden = true;
  $('#disconnect').hidden = true;
  state.accounts = [];
  state.profiles = [];
  state.runtime = null;
  state.installedModels = [];
  state.modelCatalog = [];
  state.modelProfiles = [];
  state.modelJobs = [];
  state.currentModelJob = null;
  state.runtimeError = '';
  renderRuntime();
  status('Disconnected. The management credential was cleared from this page.');
  clearError();
});

$('#provider').addEventListener('change', updateProviderFields);
$('#credential').addEventListener('input', () => $('#credential').setCustomValidity(''));

$('#add-account').addEventListener('submit', (event) => {
  event.preventDefault();
  const provider = state.providers.find((item) => item.id === $('#provider').value);
  if (!provider) return;
  const credential = $('#credential').value;
  const auth = $('#auth').value;
  if (auth === 'api-key' && !credential) {
    $('#credential').setCustomValidity('Enter a provider API key.');
    $('#credential').reportValidity();
    return;
  }
  const body = {
    provider: provider.id,
    name: $('#name').value.trim(),
    auth,
    ...(auth === 'api-key' && credential ? { credential } : {}),
    ...(auth === 'api-key' && $('#base-url').value.trim() ? { base_url: $('#base-url').value.trim() } : {}),
    ...(auth === 'oauth' ? { managed_bridge: true } : {}),
    priority: Number($('#priority').value || 0),
    protocols: provider.protocols,
  };
  void run(async () => {
    await request('/accounts', { method: 'POST', body });
    $('#credential').value = '';
    await loadAll();
  }, 'Account added. Restart the gateway to apply routing changes.');
});

async function pollOAuthLogin(accountID, sessionID) {
  const deadline = Date.now() + 10 * 60 * 1000;
  const link = $('#oauth-link');
  link.hidden = true;
  $('#login-flow').hidden = false;
  $('#login-flow-status').textContent = 'Preparing the isolated OAuth bridge. This can take a few minutes on first use.';
  $('#cancel-login').hidden = false;
  while (Date.now() < deadline) {
    try {
      const session = await request(`/accounts/${encodeURIComponent(accountID)}/login/${encodeURIComponent(sessionID)}`);
      state.loginSession = { accountID, sessionID };
      if (session.authorization_url) {
        const authorization = new URL(session.authorization_url);
        if (authorization.protocol !== 'https:' || !['accounts.google.com', 'auth.openai.com'].includes(authorization.hostname) || authorization.username || authorization.password) {
          throw new Error('Brain returned an unsafe provider sign-in URL.');
        }
        link.href = authorization.href;
        link.hidden = false;
        $('#login-flow-status').textContent = 'Open the provider sign-in link, complete authorization, then return here.';
      }
      if (session.status === 'complete') {
        link.hidden = true;
        $('#cancel-login').hidden = true;
        state.loginSession = null;
        await loadAll();
        if (session.error_code === 'model_catalog_unavailable') {
          status('Provider sign-in succeeded, but model discovery is not ready. Use Refresh models to retry.');
        } else {
          status('Provider sign-in and account model discovery completed. Restart Brain to apply routing.');
        }
        return;
      }
      if (session.status === 'failed' || session.status === 'cancelled') {
        link.hidden = true;
        $('#cancel-login').hidden = true;
        state.loginSession = null;
        status(session.status === 'cancelled' ? 'Provider sign-in was cancelled.' : `Provider sign-in failed (${session.error_code ?? 'login_failed'}).`);
        return;
      }
    } catch (error) {
      showError(error);
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 750));
  }
  showError(new Error('OAuth sign-in session expired. Start sign-in again.'));
}

async function startOAuthLogin(account) {
  let session;
  await run(async () => {
    session = await request(`/accounts/${encodeURIComponent(account.id)}/login`, { method: 'POST' });
  }, 'OAuth sign-in started.');
  if (session?.session_id) {
    state.loginSession = { accountID: account.id, sessionID: session.session_id };
    void pollOAuthLogin(account.id, session.session_id);
  }
}

$('#cancel-login').addEventListener('click', () => {
  const session = state.loginSession;
  if (!session) return;
  void run(async () => {
    await request(`/accounts/${encodeURIComponent(session.accountID)}/login/${encodeURIComponent(session.sessionID)}`, { method: 'DELETE' });
  }, 'OAuth cancellation requested.');
});

$('#refresh-service-status').addEventListener('click', () => {
  void loadServiceStatus();
});

$('#select-backend').addEventListener('click', () => {
  const backendID = $('#backend-select').value;
  if (!backendID || !window.confirm(`Select ${backendID} for Brain after restart? The configured endpoint and token stay in Brain.`)) return;
  void run(async () => {
    await request('/runtime/backend', { method: 'PUT', body: { backend_id: backendID, confirmation: backendID } });
    await loadAll();
  }, 'Backend selection saved. Restart Brain to apply it.');
});

$('#select-profile').addEventListener('click', () => {
  const profile = $('#hardware-profile').value;
  if (!profile || !window.confirm(`Set the ${profile} model profile? Required models must already be installed; Brain will restart before using this configuration.`)) return;
  void run(async () => {
    await request('/runtime/profile', { method: 'PUT', body: { profile, confirmation: profile } });
    await loadAll();
  }, 'Model profile saved. Restart Brain to apply it.');
});

$('#available-models').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action="pull-model"]');
  if (!button) return;
  const model = state.modelCatalog.find((item) => item.id === button.dataset.id);
  if (!model || !window.confirm(`Download ${model.name} (${formatBytes(model.download_bytes)}) to the Brain host? Brain requires an additional 2 GiB safety margin.`)) return;
  void startModelPull(model);
});

async function startModelPull(model) {
  let job;
  await run(async () => {
    job = await request('/model-pull-jobs', {
      method: 'POST', body: { catalog_model_id: model.id, confirmation: model.id },
    });
    state.currentModelJob = job;
    renderModelJob(job);
  }, 'Model download queued on Brain.');
  if (job?.id) void pollModelJob(job.id);
}

$('#installed-models').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action="delete-model"], button[data-action="unload-model"]');
  if (!button) return;
  const modelID = button.dataset.id;
  const unload = button.dataset.action === 'unload-model';
  if (!window.confirm(unload
    ? `Unload ${modelID} from memory? New Brain requests may load it again.`
    : `Permanently remove catalog model ${modelID} from the Brain backend? Models assigned to an active role or loaded in memory are protected.`)) return;
  void run(async () => {
    await request(`/models/${encodeURIComponent(modelID)}/${unload ? 'unload' : 'deletion'}`, {
      method: 'POST', body: { confirmation: `${unload ? 'unload' : 'delete'} ${modelID}` },
    });
    await loadRuntimeData();
  }, unload ? 'Model unloaded from memory.' : 'Model removed from the backend.');
});

$('#forge-candidates').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action]');
  if (!button) return;
  const candidateID = button.dataset.id;
  const action = button.dataset.action;
  if (action === 'forge-promote' && !window.confirm(`Promote reviewed Brain-user candidate ${candidateID}? This may replace a same-named installed skill; Brain will keep a backup.`)) return;
  if (action === 'forge-discard' && !window.confirm(`Discard Brain-local candidate ${candidateID}? No active skill or trace will be removed.`)) return;
  if (!['forge-validate', 'forge-evaluate', 'forge-promote', 'forge-discard'].includes(action)) return;
  const operation = action.slice('forge-'.length);
  const method = operation === 'discard' ? 'DELETE' : 'POST';
  const confirmation = `${operation === 'discard' ? 'discard' : operation} ${candidateID}`;
  void run(async () => {
    await request(`/forge/candidates/${encodeURIComponent(candidateID)}${operation === 'validate' || operation === 'evaluate' ? `/${operation === 'validate' ? 'validation' : 'evaluation'}` : operation === 'promote' ? '/promotion' : ''}`, {
      method, body: { confirmation },
    });
    await loadForgeData();
    if (operation === 'promote') await loadToolingData();
  }, operation === 'promote' ? 'Reviewed candidate promoted to Brain-user skills.'
    : operation === 'discard' ? 'Candidate discarded.'
      : operation === 'evaluate' ? 'Candidate evaluation updated.' : 'Candidate validation updated.');
});

$('#cancel-model-job').addEventListener('click', () => {
  const job = state.currentModelJob;
  if (!job || !['queued', 'running', 'canceling'].includes(job.status)) return;
  void run(async () => {
    state.currentModelJob = await request(`/model-pull-jobs/${encodeURIComponent(job.id)}`, { method: 'DELETE' });
    renderModelJob(state.currentModelJob);
  }, 'Model download cancellation requested.');
});

$('#knowledge-sources').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action="index-source"]');
  if (!button) return;
  const source = state.knowledgeSources.find((item) => item.id === button.dataset.id);
  if (!source || !window.confirm(`Reindex registered source ${source.name} in Brain using the local Ollama embedding model? This may update documents incrementally.`)) return;
  void startKnowledgeIndex(source);
});

async function startKnowledgeIndex(source) {
  let job;
  await run(async () => {
    job = await request('/knowledge/index-jobs', {
      method: 'POST', body: { source_id: source.id, confirmation: source.id },
    });
    state.currentKnowledgeJob = job;
    renderKnowledgeJob(job);
  }, 'Knowledge reindex queued on Brain.');
  if (job?.id) void pollKnowledgeJob(job.id);
}

$('#knowledge-search').addEventListener('submit', (event) => {
  event.preventDefault();
  const sourceID = $('#knowledge-source-select').value;
  const query = $('#knowledge-query').value.trim();
  if (!sourceID || !query) return;
  void run(async () => {
    const response = await request('/knowledge/search', {
      method: 'POST', body: { source_id: sourceID, query, limit: 5 },
    });
    state.knowledgeResults = response.results;
    renderKnowledge();
  }, 'Local knowledge search completed.');
});

$('#cancel-knowledge-job').addEventListener('click', () => {
  const job = state.currentKnowledgeJob;
  if (!job || !['queued', 'running', 'canceling'].includes(job.status)) return;
  void run(async () => {
    state.currentKnowledgeJob = await request(`/knowledge/index-jobs/${encodeURIComponent(job.id)}`, { method: 'DELETE' });
    renderKnowledgeJob(state.currentKnowledgeJob);
  }, 'Knowledge indexing cancellation requested.');
});

$('#brain-skills').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action="inspect-skill"]');
  if (!button) return;
  const skill = state.brainSkills.find((item) => item.id === button.dataset.id);
  if (skill) void loadSkillDetail(skill.id);
});

async function loadSkillDetail(id) {
  try {
    state.currentSkill = await request(`/skills/${encodeURIComponent(id)}`);
    renderTooling();
  } catch (error) {
    showError(error);
  }
}

$('#validate-skill').addEventListener('click', () => {
  const skill = state.currentSkill;
  if (!skill) return;
  void run(async () => {
    const result = await request(`/skills/${encodeURIComponent(skill.id)}/validation`);
    $('#skill-detail-validation').textContent = result.valid
      ? 'Skill passes the Agent Skills format checks.'
      : `Validation needs attention in: ${(result.failed_fields ?? []).join(', ') || 'skill metadata'}.`;
  }, 'Skill validation completed.');
});

$('#remove-skill').addEventListener('click', () => {
  const skill = state.currentSkill;
  if (!skill || !window.confirm(`Remove Brain-user skill ${skill.id}? Project/configured skills and skill references are not modified.`)) return;
  void run(async () => {
    await request(`/skills/${encodeURIComponent(skill.id)}`, {
      method: 'DELETE', body: { confirmation: `remove ${skill.id}` },
    });
    state.currentSkill = null;
    await loadToolingData();
  }, 'Brain-user skill removed.');
});

$('#brain-skill-import').addEventListener('submit', (event) => {
  event.preventDefault();
  const name = $('#new-skill-name').value.trim();
  const skillMD = $('#new-skill-markdown').value;
  if (!name || !skillMD || !window.confirm(`Install Brain-user skill ${name}? It will become available to Brain clients after installation. Ensure you trust the instructions and have removed private data.`)) return;
  void run(async () => {
    await request('/skills', {
      method: 'POST',
      body: { name, skill_md: skillMD, confirmation: `install ${name}` },
    });
    $('#new-skill-name').value = '';
    $('#new-skill-markdown').value = '';
    await loadToolingData();
  }, 'Brain validated and installed the user skill.');
});

async function pollModelJob(jobID) {
  if (state.modelPolling) return;
  state.modelPolling = true;
  try {
    while (true) {
      const job = await request(`/model-pull-jobs/${encodeURIComponent(jobID)}`);
      state.currentModelJob = job;
      renderModelJob(job);
      if (job.status === 'completed') {
        await loadRuntimeData();
        status('Model download completed on the Brain host.');
        return;
      }
      if (job.status === 'failed' || job.status === 'cancelled') {
        status(job.status === 'cancelled' ? 'Model download was cancelled.' : `Model download failed (${job.error_code ?? 'backend_pull_failed'}).`);
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } catch (error) {
    showError(error);
  } finally {
    state.modelPolling = false;
  }
}

async function pollKnowledgeJob(jobID) {
  if (state.knowledgePolling) return;
  state.knowledgePolling = true;
  try {
    while (true) {
      const job = await request(`/knowledge/index-jobs/${encodeURIComponent(jobID)}`);
      state.currentKnowledgeJob = job;
      renderKnowledgeJob(job);
      if (job.status === 'completed') {
        await loadKnowledgeData();
        status(`Knowledge indexing completed: ${job.files_indexed} files, ${job.chunks_created} chunks.`);
        return;
      }
      if (job.status === 'cancelled') {
        await loadKnowledgeData();
        status('Indexing was cancelled. Already committed files remain; reindex to converge.');
        return;
      }
      if (job.status === 'failed') {
        status(`Knowledge indexing failed (${job.error_code ?? 'knowledge_index_failed'}). Reindex to retry.`);
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  } catch (error) {
    showError(error);
  } finally {
    state.knowledgePolling = false;
  }
}

$('#local-evaluation').addEventListener('submit', (event) => {
  event.preventDefault();
  const modelID = $('#evaluation-model').value;
  if (!modelID || !window.confirm(`Run one fixed synthetic smoke prompt on local model ${modelID}? No trace, project, or client content is used.`)) return;
  void startEvaluation(modelID);
});

async function startEvaluation(modelID) {
  let job;
  await run(async () => {
    job = await request('/evaluation-jobs', {
      method: 'POST', body: { suite_id: 'local-smoke', model_id: modelID, confirmation: `eval ${modelID}` },
    });
    state.currentEvaluationJob = job;
    renderEvaluationJob(job);
  }, 'Local evaluation queued on Brain.');
  if (job?.id) void pollEvaluationJob(job.id);
}

$('#cancel-evaluation-job').addEventListener('click', () => {
  const job = state.currentEvaluationJob;
  if (!job || !['queued', 'running', 'canceling'].includes(job.status)) return;
  void run(async () => {
    state.currentEvaluationJob = await request(`/evaluation-jobs/${encodeURIComponent(job.id)}`, { method: 'DELETE' });
    renderEvaluationJob(state.currentEvaluationJob);
  }, 'Evaluation cancellation requested.');
});

async function pollEvaluationJob(jobID) {
  if (state.evaluationPolling) return;
  state.evaluationPolling = true;
  try {
    while (true) {
      const job = await request(`/evaluation-jobs/${encodeURIComponent(jobID)}`);
      state.currentEvaluationJob = job;
      renderEvaluationJob(job);
      if (job.status === 'completed') {
        await loadTraceEvalData();
        status(job.passed ? 'Local model smoke evaluation passed.' : 'Local model smoke evaluation finished with a failed assertion.');
        return;
      }
      if (job.status === 'cancelled' || job.status === 'failed') {
        status(job.status === 'cancelled' ? 'Local evaluation was cancelled.' : `Local evaluation failed (${job.error_code ?? 'evaluation_failed'}).`);
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 700));
    }
  } catch (error) {
    showError(error);
  } finally {
    state.evaluationPolling = false;
  }
}

$('#accounts').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action]');
  if (!button) return;
  const account = state.accounts.find((item) => item.id === button.dataset.id);
  if (!account) return;
  const action = button.dataset.action;
  if (action === 'login') {
    void startOAuthLogin(account);
    return;
  }
  if (action === 'remove' && !window.confirm(`Remove ${account.provider}/${account.name}? This removes only the Brain account binding. It does not revoke upstream credentials or delete private bridge credentials.`)) return;
  if (!['toggle', 'default', 'remove', 'test', 'refresh'].includes(action)) return;
  void run(async () => {
    if (action === 'remove') await request(`/accounts/${encodeURIComponent(account.id)}`, { method: 'DELETE' });
    else if (action === 'test' || action === 'refresh') {
      await request(`/accounts/${encodeURIComponent(account.id)}/${action === 'test' ? 'test' : 'models/refresh'}`, { method: 'POST' });
    }
    else await request(`/accounts/${encodeURIComponent(account.id)}`, {
      method: 'PUT', body: action === 'default' ? { make_default: true } : { enabled: !account.enabled },
    });
    await loadAll();
  }, action === 'remove'
    ? 'Account binding removed. Upstream and private bridge credentials remain unchanged.'
    : action === 'test' ? 'Account verified and models refreshed without an inference request.'
      : action === 'refresh' ? 'Provider model catalog refreshed without an inference request.'
        : 'Account updated. Restart the gateway to apply routing changes.');
});

$('#profiles').addEventListener('click', (event) => {
  const button = event.target.closest('button[data-action="save-profile"]');
  if (!button) return;
  const id = button.dataset.id;
  let profile;
  try { profile = JSON.parse($(`#profile-${CSS.escape(id)}`).value); }
  catch { showError(new Error(`Routing profile ${id} is not valid JSON.`)); return; }
  void run(async () => {
    await request(`/profiles/${encodeURIComponent(id)}`, { method: 'PUT', body: profile });
    await loadAll();
  }, 'Profile validated and saved. Update affected project policies, then restart the gateway.');
});
