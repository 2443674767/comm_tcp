import {test,expect} from '@playwright/test';

for(const [mode,expected] of [['missing','当前浏览器未提供群语音功能'],['outdated','当前浏览器的群语音功能不完整'],['insecure','请先使用 HTTPS 并信任本机证书']]){
 test(`voice preflight explains ${mode} environment without opening microphone`,async({page})=>{
  await page.addInitScript(mode=>{
   window.__micRequests=0;
   const getUserMedia=navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
   navigator.mediaDevices.getUserMedia=(...args)=>{window.__micRequests++;return getUserMedia(...args)};
   if(mode==='missing')Object.defineProperty(window,'RTCPeerConnection',{value:undefined,configurable:true});
   if(mode==='outdated')Object.defineProperty(RTCPeerConnection.prototype,'addTransceiver',{value:undefined,configurable:true});
   if(mode==='insecure')Object.defineProperty(window,'isSecureContext',{value:false,configurable:true});
  },mode);
  await page.goto('/');await page.getByLabel('你的昵称').fill('语音环境检查');await page.getByRole('button',{name:'进入聊天室',exact:true}).click();
  await expect(page.locator('#connection-label')).toHaveText('已连接');
  await page.getByRole('button',{name:'加入群语音',exact:true}).click();
  await expect(page.locator('#notice')).toContainText(expected);
  expect(await page.evaluate(()=>window.__micRequests)).toBe(0);
  await expect(page.locator('#join-voice')).toBeEnabled();await expect(page.locator('#voice-count')).toHaveText('0 / 5');
  const text=`不支持通话仍可聊天-${mode}-${Date.now()}`;
  await page.getByLabel('消息内容').fill(text);await page.getByRole('button',{name:'发送消息',exact:true}).click();
  await expect(page.getByText(text,{exact:true})).toBeVisible();
 });
}

test('diagnostic link identifies the loaded build and reports capabilities without microphone access',async({page})=>{
 await page.addInitScript(()=>{
  delete window.RTCPeerConnection;
  window.__micRequests=0;
  const original=navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
  navigator.mediaDevices.getUserMedia=(...args)=>{window.__micRequests++;return original(...args)};
 });
 await page.goto('/?diagnose=1&v=D2');await page.getByLabel('你的昵称').fill('直达检测');
 const reported=page.waitForResponse(r=>r.url().endsWith('/api/voice-support')&&r.request().method()==='POST');
 await page.getByRole('button',{name:'进入聊天室',exact:true}).click();
 const response=await reported;expect(response.status()).toBe(200);
 expect(response.request().postDataJSON()).toMatchObject({build:'voice-diagnostic-2',rtc_global:'undefined',rtc_window:'undefined',secure:true});
 await expect(page.locator('#voice-diagnostic')).toHaveText('检测版本 D2 · 记录已送达');
 await expect(page.locator('#voice-status')).toContainText('当前浏览器未提供群语音功能');
 expect(await page.evaluate(()=>window.__micRequests)).toBe(0);
});
