// Strict normalizer for the native message tool's documented direct details.
// Do not unwrap arbitrary envelopes or substitute the requested topic as proof.
function nativeTopicReceipt(sent, route) {
  if (!sent || sent.ok !== true || !sent.receipt) return null;
  const id = sent.messageId;
  if (!(typeof id === 'string' && /^[1-9][0-9]{0,18}$/.test(id)) &&
      !(typeof id === 'number' && Number.isSafeInteger(id) && id > 0)) return null;
  if (!['string', 'number'].includes(typeof sent.chatId) ||
      !['string', 'number'].includes(typeof sent.receipt.threadId) ||
      String(sent.chatId) !== route.Target ||
      String(sent.receipt.threadId) !== route.ThreadID) return null;
  return 'telegram:' + String(id) + ':topic:' + route.ThreadID;
}

// Host-tool bridge for the independently reconstructed native Go engine.
// Caller supplies a private config; no destination, credential or host path is
// embedded here. Run only after explicit user confirmation of the native test.
async function superviseNative(config) {
  if (typeof exec !== 'function' || typeof message !== 'function') {
    throw new Error('Required approved host tools are unavailable');
  }
  const { binary, state, route } = config;
  function quote(value) {
    if (typeof value !== 'string' || !value || /[\x00-\x1f]/.test(value)) {
      throw new Error('Invalid native supervisor configuration');
    }
    return "'" + value.replace(/'/g, "'\\''") + "'";
  }
  if (!route || typeof route.SessionKey !== 'string' || !route.SessionKey ||
      route.Channel !== 'telegram' || !/^-\d+$/.test(route.Target) ||
      !/^\d+$/.test(route.ThreadID) || !route.AccountID) {
    throw new Error('Invalid native supervisor route');
  }
  const base = quote(binary) + ' ';
  async function command(tail) {
    const result = await exec({ command: base + tail, title: 'Native notification state operation',
      awaitResults: true, timeoutSeconds: 110, yieldMs: 1000 });
    if (!result || result.status !== 'completed' || result.exitCode !== 0 ||
        typeof result.aggregated !== 'string') {
      throw new Error('Native notification operation failed');
    }
    let value;
    try { value = JSON.parse(result.aggregated.trim()); }
    catch (_) { throw new Error('Native notification output was invalid'); }
    if (value.success !== true) throw new Error('Native notification operation rejected');
    return value;
  }
  const batch = await command('once --state ' + quote(state));
  if (!batch.route || Object.keys(route).some(k => batch.route[k] !== route[k]) ||
      !Array.isArray(batch.outbox) || batch.outbox.length > 1000) {
    throw new Error('Native notification route or outbox mismatch');
  }
  const previous = config.previous || {};
  const health = batch.health || {status:'legacy', sources:{}};
  if (!['healthy','degraded','replay','legacy'].includes(health.status) || !health.sources ||
      typeof health.sources !== 'object' || Array.isArray(health.sources)) {
    throw new Error('Invalid native source health');
  }
  if (health.status !== 'legacy' && (Object.keys(health.sources).length !== 3 ||
      !['interaction','system','dm'].every(k => ['ok','failed','deferred'].includes(health.sources[k])))) {
    throw new Error('Invalid native source health labels');
  }
  if (config.requireHealth === true && health.status === 'legacy') {
    throw new Error('Native source health is required');
  }
  const labels = Object.values(health.sources);
  if (health.status === 'healthy' && !labels.every(x => x === 'ok') ||
      health.status === 'replay' && !labels.every(x => x === 'deferred') ||
      health.status === 'degraded' && (!labels.includes('ok') || labels.every(x => x === 'ok'))) {
    throw new Error('Native source health status is inconsistent');
  }
  const texts = {
    interaction: '主人，腾讯频道有新的互动通知。',
    system: '主人，腾讯频道有新的系统通知。',
    dm: '主人，腾讯频道私信有更新，正文未公开。'
  };
  let delivered = 0;
  // Bounded per run: unacknowledged items remain durable for the next run.
  for (const item of batch.outbox.slice(0, 8)) {
    if (!item || !/^[a-f0-9]{64}$/.test(item.delivery_id) ||
        !/^[a-f0-9]{64}$/.test(item.route_key) ||
        !Object.prototype.hasOwnProperty.call(texts, item.source)) {
      throw new Error('Native notification item was invalid');
    }
    const sent = await message({ action: 'send', channel: route.Channel,
      accountId: route.AccountID, target: route.Target, threadId: route.ThreadID,
      message: texts[item.source], final: false });
    const receipt = nativeTopicReceipt(sent, route);
    if (receipt === null) {
      throw new Error('Native notification delivery was not confirmed');
    }
    const ack = await command('ack --state ' + quote(state) + ' --key ' +
      quote(item.delivery_id) + ' --receipt ' + quote(receipt));
    if (ack.acknowledged !== true) throw new Error('Native notification acknowledgement failed');
    delivered++;
  }
  const now = Date.now();
  const oldStreak = Number.isSafeInteger(previous.degradedStreak) && previous.degradedStreak >= 0 ? previous.degradedStreak : 0;
  const degradedStreak = health.status === 'degraded' ? oldStreak + 1 : health.status === 'replay' ? oldStreak : 0;
  let lastHealthWarningAt = typeof previous.lastHealthWarningAt === 'number' && Number.isFinite(previous.lastHealthWarningAt) ? previous.lastHealthWarningAt : 0;
  if (health.status === 'degraded' && degradedStreak >= 2 &&
      (lastHealthWarningAt === 0 || now - lastHealthWarningAt >= 3600000)) {
    const warning = await message({action:'send',channel:route.Channel,
      accountId:route.AccountID,target:route.Target,threadId:route.ThreadID,
      message:'主人，部分腾讯频道通知源暂不可用；其他正常来源继续检查，失败来源的进度已保留等待重试。',final:false});
    if (nativeTopicReceipt(warning, route) === null) throw new Error('Native health warning delivery was not confirmed');
    lastHealthWarningAt = now;
  }
  return { state: { lastSuccessAt: new Date(now).toISOString(), delivered,
    pendingObserved: batch.outbox.length, deferred: Math.max(0, batch.outbox.length - delivered),
    health:health.status, sources:health.sources, degradedStreak, lastHealthWarningAt } };
}
