import {test,expect} from '@playwright/test';
const tones=page=>page.evaluate(()=>window.__tones);
 test.use({hasTouch:true,isMobile:true,viewport:{width:390,height:844},launchOptions:{args:['--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream','--autoplay-policy=user-gesture-required']}});
 test('restored phone session unlocks notifications after one tap',async({page,request})=>{
  await page.context().request.post('/api/login',{data:{nickname:'触摸用户'}});
  await page.addInitScript(()=>{window.__tones=0;const original=OscillatorNode.prototype.start;OscillatorNode.prototype.start=function(...args){window.__tones++;return original.apply(this,args)}});
  await page.goto('/');await expect(page.locator('#connection-label')).toHaveText('已连接');
  await page.getByLabel('消息内容').tap();
  await expect(page.locator('#sound-help')).toHaveText('当前频道消息与语音进出');
  await request.post('/api/login',{data:{nickname:'触摸消息发送者'}});
  await request.post('/api/messages',{data:{client_id:`touch-${Date.now()}`,text:'触摸解锁后的提示'}});
  await expect.poll(()=>tones(page)).toBe(1);
 });
