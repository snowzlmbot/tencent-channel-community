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
    if (!sent || sent.ok !== true || !sent.messageId ||
        String(sent.chatId) !== route.Target ||
        String(sent.receipt && sent.receipt.threadId) !== route.ThreadID) {
      throw new Error('Native notification delivery was not confirmed');
    }
    const receipt = 'telegram:' + String(sent.messageId) + ':topic:' + route.ThreadID;
    const ack = await command('ack --state ' + quote(state) + ' --key ' +
      quote(item.delivery_id) + ' --receipt ' + quote(receipt));
    if (ack.acknowledged !== true) throw new Error('Native notification acknowledgement failed');
    delivered++;
  }
  return { state: { lastSuccessAt: new Date().toISOString(), delivered,
    pendingObserved: batch.outbox.length, deferred: Math.max(0, batch.outbox.length - delivered) } };
}
