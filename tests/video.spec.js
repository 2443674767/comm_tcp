import {test,expect} from '@playwright/test';
async function enter(browser,name){
 const context=await browser.newContext({permissions:['microphone','camera'],viewport:{width:390,height:844}});
 const page=await context.newPage();
 await page.addInitScript(()=>{const PC=window.RTCPeerConnection;window.peers=[];window.RTCPeerConnection=class extends PC{constructor(...a){super(...a);window.peers.push(this)}};window.streams=[];const gum=navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);navigator.mediaDevices.getUserMedia=async c=>{const s=await gum(c);window.streams.push(s);return s}});
 await page.goto('/');await page.locator('#nickname').fill(name);await page.locator('#login-submit').click();await expect(page.locator('#connection-label')).toHaveText('已连接');
 await page.locator('#join-voice').click();await expect(page.locator('#voice-status')).toHaveText('通话中');return {context,page};
}
const frames=page=>page.evaluate(async()=>{let n=0;(await window.peers.at(-1).getStats()).forEach(s=>{if(s.type==='inbound-rtp'&&s.kind==='video')n+=s.framesDecoded||0});return n});
test('camera off receiver decodes late publisher, camera restart and reused slot',async({browser})=>{
 const a=await enter(browser,'视频甲'),b=await enter(browser,'视频乙');
 try{
  await expect(a.page.locator('#camera-toggle')).toHaveText('开启摄像头');
  expect(await a.page.evaluate(()=>window.streams.flatMap(s=>s.getVideoTracks()).length)).toBe(0);
  await a.page.locator('#camera-toggle').click();
  await expect.poll(()=>frames(b.page),{timeout:15000}).toBeGreaterThan(5);
  await expect(b.page.locator('.video-tile').filter({hasText:'视频甲'})).toHaveClass(/has-video/);
  await b.page.screenshot({path:'.work/video-release/mobile-video.png',fullPage:true});
  const c=await enter(browser,'视频丙');
  try{await expect.poll(()=>frames(c.page),{timeout:15000}).toBeGreaterThan(5);}finally{await c.context.close()}
  await b.page.locator('#camera-toggle').click();await expect.poll(()=>frames(a.page),{timeout:15000}).toBeGreaterThan(5);
  await a.page.locator('#camera-toggle').click();
  await expect.poll(()=>a.page.evaluate(()=>window.streams.flatMap(s=>s.getVideoTracks()).every(t=>t.readyState==='ended'))).toBe(true);
  await expect(b.page.locator('.video-tile').filter({hasText:'视频甲'})).not.toHaveClass(/has-video/);
  const n=await frames(b.page);await a.page.locator('#camera-toggle').click();await expect.poll(()=>frames(b.page),{timeout:15000}).toBeGreaterThan(n+5);
  await a.page.locator('#leave-voice').click();await expect(b.page.locator('#voice-count')).toHaveText('1 / 5');
  const d=await enter(browser,'视频替补');
  try{const before=await frames(b.page);await d.page.locator('#camera-toggle').click();await expect.poll(()=>frames(b.page),{timeout:15000}).toBeGreaterThan(before+5);await expect(b.page.locator('.video-tile')).toContainText(['视频乙','视频替补']);}finally{await d.context.close()}
  expect(await b.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 }finally{await a.context.close();await b.context.close()}
});
test('camera denial preserves audio and leave cancels pending camera acquisition',async({browser})=>{
 const a=await enter(browser,'权限视频');
 try{
  await a.page.evaluate(()=>{window.realGum=navigator.mediaDevices.getUserMedia;navigator.mediaDevices.getUserMedia=async c=>{if(c.video)throw new DOMException('denied','NotAllowedError');return window.realGum(c)}});
  await a.page.locator('#camera-toggle').click();await expect(a.page.locator('#notice')).toContainText('摄像头');await expect(a.page.locator('#voice-status')).toHaveText('通话中');
  await a.page.evaluate(()=>{navigator.mediaDevices.getUserMedia=window.realGum});
  await a.page.locator('#camera-toggle').click();await expect(a.page.locator('#camera-toggle')).toHaveText('关闭摄像头');
  await a.page.evaluate(()=>{navigator.mediaDevices.getUserMedia=async c=>{if(c.video?.facingMode?.exact)throw new DOMException('no rear camera','OverconstrainedError');return window.realGum(c)}});
  await a.page.locator('#camera-switch').click();await expect(a.page.locator('#notice')).toContainText('切换失败');await expect(a.page.locator('#voice-status')).toHaveText('通话中');
  await a.page.evaluate(()=>{navigator.mediaDevices.getUserMedia=async c=>{const s=await window.realGum(c);await new Promise(r=>window.releaseCamera=r);return s}});
  await a.page.locator('#camera-toggle').click();await expect.poll(()=>a.page.evaluate(()=>typeof window.releaseCamera)).toBe('function');await a.page.locator('#leave-voice').click();await a.page.evaluate(()=>window.releaseCamera());
  await expect.poll(()=>a.page.evaluate(()=>window.streams.flatMap(s=>s.getTracks()).every(t=>t.readyState==='ended'))).toBe(true);
 }finally{await a.context.close()}
});
