const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../automation/native-notifications.js'), 'utf8');
const route = {SessionKey:'synthetic-session',Channel:'telegram',Target:'-100',ThreadID:'42',AccountID:'test'};
const item = {delivery_id:'a'.repeat(64),route_key:'b'.repeat(64),source:'dm',text:'must-not-forward-this-private-body'};
const config = {binary:'/private/test/native',state:'/private/test/state.json',route};
function fixture(options={}) {
  const commands=[], sends=[];
  const sandbox = {Date, exec:async args=>{
    commands.push(args);
    if(options.execFail) return {status:'completed',exitCode:1,aggregated:'private-error'};
    const ack=args.command.includes(' ack ');
    return {status:'completed',exitCode:0,aggregated:JSON.stringify(ack?
      {success:true,acknowledged:!options.ackFail}:
      {success:true,route:options.route||route,outbox:options.outbox||[]})};
  },message:async args=>{
    sends.push(args);
    return {ok:!options.sendFail,messageId:'123',chatId:route.Target,receipt:{threadId:options.wrongTopic?'999':route.ThreadID}};
  }};
  const run=vm.runInNewContext(source+'\nsuperviseNative;',sandbox);
  return {run,commands,sends};
}
test('quiet native poll uses no messaging or model work',async()=>{
  const f=fixture(); const result=await f.run(config);
  assert.equal(result.state.delivered,0);assert.equal(f.commands.length,1);assert.equal(f.sends.length,0);
  assert.equal(f.commands[0].awaitResults,true);
});
test('confirmed tool receipt precedes exact native ack; content stays generic',async()=>{
  const f=fixture({outbox:[item]});const result=await f.run(config);
  assert.equal(result.state.delivered,1);assert.equal(f.sends.length,1);assert.equal(f.commands.length,2);
  assert.equal(f.sends[0].threadId,'42');assert.equal(f.sends[0].target,'-100');
  assert(!f.sends[0].message.includes(item.text));assert(f.commands[1].command.includes('telegram:123:topic:42'));
  assert(f.commands[1].command.includes(item.delivery_id));
});
test('unconfirmed delivery does not acknowledge pending record',async()=>{
  for(const options of [{sendFail:true},{wrongTopic:true}]){
    const f=fixture({...options,outbox:[item]});await assert.rejects(f.run(config));assert.equal(f.commands.length,1);
  }
});
test('route or unknown source mismatch fails before message send',async()=>{
  for(const options of [{route:{...route,Target:'-200'},outbox:[item]},{outbox:[{...item,source:'__proto__'}]}]){
    const f=fixture(options);await assert.rejects(f.run(config));assert.equal(f.sends.length,0);
  }
});
test('native operation failure cannot be silently marked successful',async()=>{
  const f=fixture({execFail:true});await assert.rejects(f.run(config));assert.equal(f.sends.length,0);
});
test('bounded delivery leaves remaining durable records for next run',async()=>{
  const f=fixture({outbox:Array.from({length:10},(_,i)=>({...item,delivery_id:(i+1).toString(16).padStart(64,'0')}))});const result=await f.run(config);
  assert.equal(f.sends.length,8);assert.equal(result.state.deferred,2);assert.equal(f.commands.length,9);assert.equal(f.commands.length+f.sends.length,17);
});
