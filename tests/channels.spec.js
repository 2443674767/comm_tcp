import {test,expect} from '@playwright/test';

async function login(page,name){
 await page.addInitScript(()=>{
  window.__tones=0;
  const start=OscillatorNode.prototype.start;
  OscillatorNode.prototype.start=function(...args){window.__tones++;return start.apply(this,args)};
 });
 await page.goto('/');await page.getByLabel('你的昵称').fill(name);await page.getByRole('button',{name:'进入聊天室',exact:true}).click();await expect(page.locator('#connection-label')).toHaveText('已连接');
}
const tones=page=>page.evaluate(()=>window.__tones);

test('channels isolate messages and notify all current-channel observers of voice joins and leaves',async({browser})=>{
 const contexts=await Promise.all([{}, {viewport:{width:390,height:844}},{}].map(options=>browser.newContext({...options,permissions:['microphone']})));
 const [p,q,r]=await Promise.all(contexts.map(c=>c.newPage()));
 try{
  await login(p,'频道发言');await login(q,'频道旁听');await login(r,'其他频道');
  await p.getByRole('button',{name:'创建频道',exact:true}).click();const name=`讨论-${Date.now()}`;
  await p.getByLabel('频道名称').fill(name);await p.getByRole('button',{name:'确认创建',exact:true}).click();
  await expect(p.locator('#channel-title')).toHaveText(name);
  await expect(q.locator('#channel-select option').filter({hasText:name})).toHaveCount(1);
  await q.getByLabel('当前频道').selectOption({label:name});await expect(q.locator('#connection-label')).toHaveText('已连接');
  await q.getByLabel('消息内容').click(); // unlock on restored sessions too
  const before=await tones(q), other=await tones(r);
  const text=`频道专属-${Date.now()}`;
  await p.getByLabel('消息内容').fill(text);await p.getByRole('button',{name:'发送消息',exact:true}).click();
  await expect(q.getByText(text,{exact:true})).toBeVisible();await expect.poll(()=>tones(q)).toBe(before+1);
  await expect(r.getByText(text,{exact:true})).toHaveCount(0);expect(await tones(r)).toBe(other);
  await p.getByRole('button',{name:'加入群语音',exact:true}).click();await expect(p.locator('#voice-status')).toHaveText('通话中');
  await expect(q.locator('#voice-count')).toHaveText('1 / 5');// Real OS speech can serialize utterances from both browser contexts. Wait for speech to finish before checking tones.
  await expect.poll(()=>tones(q),{timeout:15000}).toBe(before+3);
  await expect(q.locator('#join-voice')).toBeVisible(); // observer never joins call
  await expect(r.locator('#voice-count')).toHaveText('0 / 5');expect(await tones(r)).toBe(other);
  await p.getByLabel('当前频道').selectOption('public');
  await expect(q.locator('#voice-count')).toHaveText('0 / 5');await expect.poll(()=>tones(q),{timeout:15000}).toBe(before+4);
  await q.reload();await expect(q.getByText(text,{exact:true})).toBeVisible();expect(await tones(q)).toBe(0);
  expect(await q.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await q.screenshot({path:'test-results/channels-mobile.png',fullPage:true});
  await q.getByRole('button',{name:'关闭提示音',exact:true}).click();
  await p.getByLabel('当前频道').selectOption({label:name});await expect(p.getByText(text,{exact:true})).toBeVisible();
  await p.getByLabel('消息内容').fill('静音后的消息');await p.getByRole('button',{name:'发送消息',exact:true}).click();
  await expect(q.getByText('静音后的消息',{exact:true})).toBeVisible();expect(await tones(q)).toBe(0);
 } finally {await Promise.all(contexts.map(c=>c.close()))}
});

test('live notification survives history arriving first; history, rosters and duplicate events stay silent',async({page})=>{
 let socket;let connections=0;
 const message={id:99999,channel_id:'public',client_id:'race',session_id:'someone',nickname:'伙伴',kind:'text',text:'历史与实时同时到达',created_at:'2026-11-10T12:00:00Z'};
 await page.routeWebSocket('**/ws',ws=>{socket=ws;connections++;ws.send(JSON.stringify({type:'ready'}));ws.send(JSON.stringify({type:'voice',members:[{id:'someone',nickname:'伙伴',slot:0}]}));});
 await page.route('**/api/messages*',route=>route.fulfill({json:[message]}));
 await login(page,'实时提示');await expect(page.getByText(message.text,{exact:true})).toBeVisible();expect(await tones(page)).toBe(0);
 socket.send(JSON.stringify({type:'message',message}));await expect.poll(()=>tones(page)).toBe(1);
 socket.send(JSON.stringify({type:'message',message}));socket.close();await expect.poll(()=>connections).toBe(2);
 await expect(page.locator('#connection-label')).toHaveText('已连接');expect(await tones(page)).toBe(1);
});

test('delayed channel list cannot remove the channel just created',async({page})=>{
 let count=0,release;
 await page.route('**/api/channels',async route=>{
  if(route.request().method()!=='GET')return route.continue();
  count++;if(count===2){await new Promise(resolve=>release=resolve);return route.fulfill({json:[{id:'public',name:'公共频道'}]});}
  return route.continue();
 });
 await login(page,'乱序频道');await expect.poll(()=>Boolean(release)).toBe(true);
 await page.getByRole('button',{name:'创建频道',exact:true}).click();const name=`保留-${Date.now()}`;
 await page.getByLabel('频道名称').fill(name);await page.getByRole('button',{name:'确认创建',exact:true}).click();
 await expect(page.locator('#channel-title')).toHaveText(name);release();await page.waitForTimeout(200);
 await expect(page.locator('#channel-title')).toHaveText(name);
 await expect(page.locator('#channel-select option:checked')).toHaveText(name);
});

test('switching channels discards a delayed history response',async({page})=>{
 let release;
 await page.route('**/api/messages*',async route=>{
  if(new URL(route.request().url()).searchParams.has('channel_id'))return route.continue();
  await new Promise(resolve=>release=resolve);
  return route.fulfill({json:[{id:88888,channel_id:'public',session_id:'someone',nickname:'伙伴',kind:'text',text:'不该出现的旧频道历史',created_at:'2026-11-10T12:00:00Z'}]});
 });
 await login(page,'切换历史');await expect.poll(()=>Boolean(release)).toBe(true);
 await page.getByRole('button',{name:'创建频道',exact:true}).click();await page.getByLabel('频道名称').fill(`慢历史-${Date.now()}`);await page.getByRole('button',{name:'确认创建',exact:true}).click();
 await expect(page.locator('#channel-select')).not.toHaveValue('public');await expect(page.locator('#connection-label')).toHaveText('已连接');release();await page.waitForTimeout(150);
 await expect(page.getByText('不该出现的旧频道历史',{exact:true})).toHaveCount(0);
});
