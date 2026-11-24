import { test, expect } from '@playwright/test';

async function enter(page, name) {
  await page.addInitScript(() => { const Original = window.RTCPeerConnection; window.__testPeers=[]; window.RTCPeerConnection=class extends Original { constructor(...args) { super(...args); window.__testPeers.push(this); } }; });
  await page.goto('/');
  await page.getByLabel('你的昵称').fill(name);
  await page.getByRole('button', {name:'进入聊天室',exact:true}).click();
  await expect(page.locator('#connection-label')).toHaveText('已连接');
}

test('desktop and phone share messages, history, recording and server voice', async ({browser}) => {
  const a = await browser.newContext({permissions:['microphone']});
  const b = await browser.newContext({viewport:{width:390,height:844},permissions:['microphone']});
  const p = await a.newPage(), q = await b.newPage();
  const errors=[];p.on('pageerror',e=>errors.push(e.message));q.on('pageerror',e=>errors.push(e.message));
  await enter(p,'桌面用户');await enter(q,'手机用户');
  const text=`你好，局域网！${Date.now()}`;
  await p.getByLabel('消息内容').fill(text);await p.getByRole('button',{name:'发送消息',exact:true}).click();
  await expect(q.locator('.message-text').filter({hasText:text})).toBeVisible();
  await q.reload();await expect(q.locator('.message-text').filter({hasText:text})).toBeVisible();
  expect(await q.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await p.getByRole('button',{name:'录制语音',exact:true}).click();
  await expect(p.locator('#record-panel')).toBeVisible();
  await p.waitForTimeout(1100);
  await p.getByRole('button',{name:'结束录音',exact:true}).click();
  await expect(p.getByRole('button',{name:'发送录音',exact:true})).toBeEnabled();
  const before=await q.locator('.message audio').count();
  await p.getByRole('button',{name:'发送录音',exact:true}).click();
  await expect(q.locator('.message audio')).toHaveCount(before+1);
  await q.locator('.message audio').last().evaluate(audio=>audio.play());
  await expect.poll(()=>q.locator('.message audio').last().evaluate(audio=>audio.currentTime)).toBeGreaterThan(0);
  await p.getByRole('button',{name:'加入群语音',exact:true}).click();
  await expect(p.locator('#voice-status')).toHaveText('通话中');
  await q.getByRole('button',{name:'加入群语音',exact:true}).click();
  await expect(q.locator('#voice-status')).toHaveText('通话中');
  await expect(p.locator('#voice-count')).toHaveText('2 / 5');
  // Receiving actual RTP proves this is a server media path, not just a joined UI.
  await expect.poll(()=>q.evaluate(async()=>{
    const stats=await window.__testPeers.at(-1).getStats();let packets=0;
    stats.forEach(s=>{if(s.type==='inbound-rtp'&&s.kind==='audio')packets+=s.packetsReceived||0});return packets;
  })).toBeGreaterThan(0);
  await p.getByRole('button',{name:'静音麦克风',exact:true}).click();
  await expect(p.getByRole('button',{name:'开启麦克风',exact:true})).toBeVisible();
  await p.screenshot({path:'test-results/desktop.png',fullPage:true});
  await q.screenshot({path:'test-results/mobile.png',fullPage:true});
  await p.getByRole('button',{name:'退出群语音',exact:true}).click();
  await expect(q.locator('#voice-count')).toHaveText('1 / 5');
  await p.getByRole('button',{name:'加入群语音',exact:true}).click();
  await expect(p.locator('#voice-status')).toHaveText('通话中');
  await q.getByRole('button',{name:'退出登录',exact:true}).click();
  await expect(q.getByLabel('你的昵称')).toBeVisible();
  expect(errors).toEqual([]);
  await a.close();await b.close();
});

test('microphone denial is recoverable and messages render as text', async({page})=>{
  await page.addInitScript(()=>{navigator.mediaDevices.getUserMedia=async()=>{throw new DOMException('denied','NotAllowedError')}});
  await enter(page,'权限测试');
  await page.getByRole('button',{name:'录制语音',exact:true}).click();
  await expect(page.locator('#notice')).toContainText('麦克风');
  await expect(page.getByRole('button',{name:'录制语音',exact:true})).toBeEnabled();
  const literal='<img src=x onerror=alert(1)>';
  await page.getByLabel('消息内容').fill(literal);await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await expect(page.locator('.message-text').filter({hasText:literal}).last()).toBeVisible();
  expect(await page.locator('.message img').count()).toBe(0);
});

test('live message arriving before reconnect-ready cannot skip missed history',async({page})=>{
  const make=(n)=>({id:n,client_id:`fixture-${n}`,session_id:'someone',nickname:'伙伴',kind:'text',text:`重连历史-${n}`,created_at:'2026-11-09T12:00:00Z'});
  let socket;let connections=0;
  await page.routeWebSocket('**/ws',ws=>{socket=ws;connections++;if(connections>1)ws.send(JSON.stringify({type:'message',message:make(3)}));ws.send(JSON.stringify({type:'ready'}));});
  await page.route('**/api/messages*',route=>{
    const url=new URL(route.request().url());const after=url.searchParams.get('after');
    const messages=after===null?[make(1)]:Number(after)<2?[make(2),make(3)]:[];
    return route.fulfill({json:messages});
  });
  await enter(page,'补齐测试');await expect(page.getByText('重连历史-1',{exact:true})).toBeVisible();
  socket.close();
  await expect(page.getByText('重连历史-2',{exact:true})).toBeVisible({timeout:10000});
  await expect(page.getByText('重连历史-3',{exact:true})).toBeVisible();
});

test('shared tabs adopt the new login session after reconnect',async({page,context})=>{
  await enter(page,'旧昵称');
  const tab=await context.newPage();await tab.goto('/');await expect(tab.locator('#my-name')).toHaveText('旧昵称');
  await page.getByRole('button',{name:'退出登录',exact:true}).click();
  await page.getByLabel('你的昵称').fill('新昵称');await page.getByRole('button',{name:'进入聊天室',exact:true}).click();
  await expect(tab.locator('#my-name')).toHaveText('新昵称',{timeout:10000});
});

test('closing a second tab during microphone permission does not end the first call',async({browser})=>{
  const context=await browser.newContext({permissions:['microphone']});const first=await context.newPage();
  await enter(first,'多页通话');await first.getByRole('button',{name:'加入群语音',exact:true}).click();await expect(first.locator('#voice-status')).toHaveText('通话中');
  const second=await context.newPage();await second.addInitScript(()=>{navigator.mediaDevices.getUserMedia=()=>new Promise(()=>{});});
  await second.goto('/');await expect(second.locator('#connection-label')).toHaveText('已连接');
  await second.getByRole('button',{name:'加入群语音',exact:true}).click();await second.goto('about:blank');
  await first.waitForTimeout(1000);
  const members=await first.evaluate(async()=> (await fetch('/api/voice')).json());
  expect(members).toHaveLength(1);
  await context.close();
});

test('reconnect during an active history fetch queues another synchronization',async({page})=>{
  const make=n=>({id:n,client_id:`delayed-${n}`,session_id:'someone',nickname:'伙伴',kind:'text',text:`延迟历史-${n}`,created_at:'2026-11-09T12:00:00Z'});
  let socket,release;let connections=0;let requests=0;
  await page.routeWebSocket('**/ws',ws=>{socket=ws;connections++;ws.send(JSON.stringify({type:'ready'}));});
  await page.route('**/api/messages*',async route=>{
    requests++;
    if(requests===1){await new Promise(resolve=>{release=resolve;});return route.fulfill({json:[make(1)]});}
    return route.fulfill({json:[make(2)]});
  });
  await enter(page,'慢网络测试');await expect.poll(()=>Boolean(release)).toBe(true);
  socket.close();await expect.poll(()=>connections).toBe(2);
  // Allow the second ready handler to run while the first HTTP snapshot is held.
  await page.waitForTimeout(100);release();
  await expect(page.getByText('延迟历史-2',{exact:true})).toBeVisible({timeout:10000});
  expect(requests).toBe(2);
});
