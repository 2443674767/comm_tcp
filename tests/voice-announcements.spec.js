import {test,expect} from '@playwright/test';

async function setup(page,{unsupported=false}={}){
 await page.addInitScript(unsupported=>{
  window.__spoken=[];window.__utterances=[];window.__tones=0;window.__cancelled=0;window.__voices=[{name:'Chinese',lang:'zh-CN',localService:true}];
  const start=OscillatorNode.prototype.start;
  OscillatorNode.prototype.start=function(...args){window.__tones++;return start.apply(this,args)};
  Object.defineProperty(window,'SpeechSynthesisUtterance',{configurable:true,value:class{constructor(text){this.text=text}}});
  Object.defineProperty(window,'speechSynthesis',{configurable:true,value:unsupported?undefined:Object.assign(new EventTarget(),{
   getVoices:()=>window.__voices,
   speak:utterance=>{window.__spoken.push({text:utterance.text,lang:utterance.lang,voice:utterance.voice?.name});window.__utterances.push(utterance);utterance.onstart?.();},
   cancel:()=>{window.__cancelled++}
  })});
 },unsupported);
 let socket;
 await page.routeWebSocket('**/ws*',ws=>{socket=ws;ws.send(JSON.stringify({type:'ready'}));});
 await page.route('**/api/channels',route=>route.fulfill({json:[{id:'public',name:'公共频道'},{id:'second',name:'第二频道'}]}));
 await page.route('**/api/messages*',route=>route.fulfill({json:[]}));
 await page.goto('/');await page.getByLabel('你的昵称').fill('未加入语音的观察者');await page.getByRole('button',{name:'进入聊天室',exact:true}).click();
 await expect(page.locator('#connection-label')).toHaveText('已连接');
 return (action,name='小明')=>socket.send(JSON.stringify({type:'voice_event',action,member:{id:'someone',nickname:name}}));
}
const tones=page=>page.evaluate(()=>window.__tones);
const spoken=page=>page.evaluate(()=>window.__spoken);

test('all channel observers hear nickname first, then two join tones or one leave tone in order',async({page})=>{
 const send=await setup(page);send('join','小明');send('leave','小红');
 await expect.poll(()=>spoken(page)).toEqual([{text:'小明加入语音',lang:'zh-CN',voice:'Chinese'}]);
 expect(await tones(page)).toBe(0);
 await page.evaluate(()=>window.__utterances[0].onend());
 await expect.poll(()=>tones(page)).toBe(2);
 await expect.poll(()=>spoken(page)).toEqual([{text:'小明加入语音',lang:'zh-CN',voice:'Chinese'},{text:'小红退出语音',lang:'zh-CN',voice:'Chinese'}]);
 await page.evaluate(()=>window.__utterances[1].onend());await expect.poll(()=>tones(page)).toBe(3);
 await expect(page.locator('#join-voice')).toBeVisible();
});

for(const operation of ['mute','switch','logout']){
 test(`${operation} cancels speech and queued notifications, ignoring late speech completion`,async({page})=>{
  const send=await setup(page);send('join');send('leave');
  await expect.poll(()=>spoken(page)).toHaveLength(1);
  await page.evaluate(()=>window.__lateEnd=window.__utterances[0].onend);
  if(operation==='mute')await page.getByRole('button',{name:'关闭提示音',exact:true}).click();
  if(operation==='switch')await page.getByLabel('当前频道').selectOption('second');
  if(operation==='logout'){await page.getByRole('button',{name:'退出登录',exact:true}).click();await expect(page.getByLabel('你的昵称')).toBeVisible();}
  await page.evaluate(()=>window.__lateEnd());await page.waitForTimeout(300);
  expect(await tones(page)).toBe(0);expect(await spoken(page)).toHaveLength(1);
  expect(await page.evaluate(()=>window.__cancelled)).toBeGreaterThan(0);
 });
}

test('unavailable speech still plays the requested two and one tones',async({page})=>{
 const send=await setup(page,{unsupported:true});send('join');await expect.poll(()=>tones(page)).toBe(2);
 send('leave');await expect.poll(()=>tones(page)).toBe(3);
 await expect(page.locator('#sound-help')).toContainText('播报不可用');
});

test('speech failure falls back once and does not block the next announcement',async({page})=>{
 const send=await setup(page);send('join');send('leave');await expect.poll(()=>spoken(page)).toHaveLength(1);
 await page.evaluate(()=>{const utterance=window.__utterances[0];const end=utterance.onend;utterance.onerror({error:'synthesis-failed'});end();});
 await expect.poll(()=>tones(page)).toBe(2);await expect.poll(()=>spoken(page)).toHaveLength(2);
 await page.evaluate(()=>window.__utterances[1].onend());await expect.poll(()=>tones(page)).toBe(3);
});


test('late-loading local Chinese voice is used before the join tones',async({page})=>{
 const send=await setup(page);await page.evaluate(()=>window.__voices=[]);send('join');
 await expect(page.locator('#notice')).toContainText('加入');expect(await tones(page)).toBe(0);expect(await spoken(page)).toHaveLength(0);
 await page.evaluate(()=>{window.__voices=[{name:'Chinese',lang:'zh-CN',localService:true}];speechSynthesis.dispatchEvent(new Event('voiceschanged'));});
 await expect.poll(()=>spoken(page)).toHaveLength(1);await page.evaluate(()=>window.__utterances[0].onend());await expect.poll(()=>tones(page)).toBe(2);
});

test('nickname is never sent to a remote speech voice',async({page})=>{
 const send=await setup(page);await page.evaluate(()=>window.__voices=[{name:'Remote Chinese',lang:'zh-CN',localService:false}]);send('join');
 await expect.poll(()=>tones(page)).toBe(2);expect(await spoken(page)).toHaveLength(0);
 await expect(page.locator('#sound-help')).toContainText('播报不可用');
});
