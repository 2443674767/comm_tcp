import {test,expect} from '@playwright/test';

// The chat server is replaced by routes so every invitation event can be driven
// exactly; a fake peer connection stands in for the real voice negotiation.
const me={id:'me',nickname:'我自己'};
const users=[me,{id:'ann',nickname:'小安'},{id:'bob',nickname:'小博'},{id:'cat',nickname:'小陈'}];
// Everyone online, wherever they are: Bob is in another channel.
const online=users.map(user=>({...user,channel_id:user.id==='bob'?'second':'public'}));
async function setup(page,{joinFails=false}={}){
 await page.addInitScript(()=>{
  window.__tones=0;const start=OscillatorNode.prototype.start;
  OscillatorNode.prototype.start=function(...args){window.__tones++;return start.apply(this,args)};
  window.RTCPeerConnection=class{
   constructor(){this.iceGatheringState='complete';this.connectionState='connected';this.localDescription=null;}
   addTransceiver(){return {sender:{replaceTrack:async()=>{},getParameters:()=>({encodings:[]}),setParameters:async()=>{}},setCodecPreferences(){}}} addEventListener(){} removeEventListener(){} close(){}
   async createOffer(){return {type:'offer',sdp:'fake-offer'}}
   async setLocalDescription(description){this.localDescription=description}
   async setRemoteDescription(){}
  };
 });
 const calls={voice:0,voiceChannels:[],invites:[],replies:[]};let socket;
 await page.routeWebSocket('**/ws*',ws=>{socket=ws;ws.send(JSON.stringify({type:'ready'}));ws.send(JSON.stringify({type:'presence',users:users.filter(user=>user.id!=='bob')}));ws.send(JSON.stringify({type:'online',users:online}));ws.send(JSON.stringify({type:'voice',members:[{id:'cat',nickname:'小陈'}]}));});
 await page.route('**/api/me',route=>route.fulfill({status:401,json:{error:'请先输入昵称登录'}}));
 await page.route('**/api/login',route=>route.fulfill({json:me}));
 await page.route('**/api/channels',route=>route.fulfill({json:[{id:'public',name:'公共频道'},{id:'second',name:'第二频道'}]}));
 await page.route('**/api/messages*',route=>route.fulfill({json:[]}));
 await page.route(/\/api\/voice/,route=>{
  const url=new URL(route.request().url());
  if(url.pathname==='/api/voice-support')return route.fulfill({json:{ok:true}});
  if(url.pathname==='/api/voice-invite/reply'){calls.replies.push(route.request().postDataJSON());return route.fulfill({json:{ok:true}});}
  if(url.pathname==='/api/voice-invite'){const body=route.request().postDataJSON();calls.invites.push(body);return route.fulfill({json:{invited:users.filter(user=>body.targets.includes(user.id)),skipped:[]}});}
  if(route.request().method()==='POST'){calls.voice++;calls.voiceChannels.push(url.searchParams.get('channel_id')||'public');return joinFails?route.fulfill({status:400,json:{error:'群语音人数已满'}}):route.fulfill({json:{type:'answer',sdp:'fake-answer'}});}
  return route.fulfill({json:{ok:true}});
 });
 await page.goto('/');await page.getByLabel('你的昵称').fill(me.nickname);await page.getByRole('button',{name:'进入聊天室',exact:true}).click();
 await expect(page.locator('#connection-label')).toHaveText('已连接');
 const send=event=>socket.send(JSON.stringify(event));
 const ring=(id='invite-1',from=users[1],channel='public')=>send({type:'voice_invite',invite_id:id,channel_id:channel,from,expires_in:30});
 return {calls,send,ring};
}
const tones=page=>page.evaluate(()=>window.__tones);
const card=page=>page.getByRole('alertdialog');

test('an invitation rings until it is declined, and declining tells the server',async({page})=>{
 const {calls,ring}=await setup(page);ring();
 await expect(card(page)).toContainText('小安');await expect(card(page)).toContainText('邀请你加入「公共频道」群语音');
 await expect.poll(()=>tones(page)).toBe(4);await expect(page).toHaveTitle(/语音邀请/);
 await expect.poll(()=>tones(page),{timeout:5000}).toBe(8); // the ring repeats while unanswered
 await card(page).getByRole('button',{name:'拒绝',exact:true}).click();
 await expect(card(page)).toHaveCount(0);await expect(page).toHaveTitle('邻语 · 局域网聊天室');
 expect(calls.replies).toEqual([{invite_id:'invite-1',action:'decline'}]);
 const after=await tones(page);await page.waitForTimeout(2500);expect(await tones(page)).toBe(after);
 expect(calls.voice).toBe(0);await expect(page.locator('#join-voice')).toBeVisible();
});

test('with notification sound off the invitation shows without ringing',async({page})=>{
 const {ring}=await setup(page);
 await page.getByRole('button',{name:'关闭提示音',exact:true}).click();ring();
 await expect(card(page)).toBeVisible();await page.waitForTimeout(2500);expect(await tones(page)).toBe(0);
});

test('accepting joins voice and needs no separate reply',async({page})=>{
 const {calls,ring,send}=await setup(page);ring();
 await card(page).getByRole('button',{name:'接受并加入',exact:true}).click();
 await expect(card(page)).toHaveCount(0);await expect(page.locator('#leave-voice')).toBeVisible();
 expect(calls.voice).toBe(1);expect(calls.replies).toEqual([]);
 send({type:'voice_invite_end',invite_id:'invite-1',reason:'accept'}); // late settle from the server is harmless
 await expect(page.locator('#voice-status')).toHaveText('通话中');
});

test('accepting when the join fails reports the failure to the inviter',async({page})=>{
 const {calls,ring}=await setup(page,{joinFails:true});ring();
 await card(page).getByRole('button',{name:'接受并加入',exact:true}).click();
 await expect(page.locator('#notice')).toContainText('群语音人数已满');
 await expect.poll(()=>calls.replies).toEqual([{invite_id:'invite-1',action:'failed'}]);
 await expect(page.locator('#join-voice')).toBeVisible();
});

test('cancel and timeout stop the ring; switching channel keeps an invitation',async({page})=>{
 const {send,ring}=await setup(page);ring('a');ring('b',users[2]);
 await expect(card(page)).toHaveCount(2);
 send({type:'voice_invite_end',invite_id:'a',reason:'cancelled'});
 await expect(card(page)).toHaveCount(1);await expect(page.locator('#notice')).toHaveText('小安 已取消语音邀请');
 await expect(page).toHaveTitle(/语音邀请/); // the other one still rings
 send({type:'voice_invite_end',invite_id:'b',reason:'timeout'});
 await expect(card(page)).toHaveCount(0);await expect(page.locator('#notice')).toHaveText('未接听 小博 的语音邀请');
 await expect(page).toHaveTitle('邻语 · 局域网聊天室');
 ring('c');await expect(card(page)).toHaveCount(1);
 await page.getByLabel('当前频道').selectOption('second');await expect(page.locator('#channel-title')).toHaveText('第二频道');
 await expect(card(page)).toHaveCount(1);await expect(page).toHaveTitle(/语音邀请/);
});

test('accepting an invitation from another channel switches there and joins its voice',async({page})=>{
 const {calls,ring}=await setup(page);ring('x',users[2],'second');
 await expect(card(page)).toContainText('邀请你加入「第二频道」群语音，接受后自动切换到该频道');
 await card(page).getByRole('button',{name:'接受并加入',exact:true}).click();
 await expect(page.locator('#channel-title')).toHaveText('第二频道');await expect(page.getByLabel('当前频道')).toHaveValue('second');
 await expect(page.locator('#leave-voice')).toBeVisible();await expect(page.locator('#voice-status')).toHaveText('通话中');
 expect(calls.voiceChannels).toEqual(['second']);expect(calls.replies).toEqual([]);
 await expect(card(page)).toHaveCount(0);await expect(page).toHaveTitle('邻语 · 局域网聊天室');
});

test('declining an invitation from another channel stays put',async({page})=>{
 const {calls,ring}=await setup(page);ring('x',users[2],'second');
 await card(page).getByRole('button',{name:'拒绝',exact:true}).click();
 await expect(card(page)).toHaveCount(0);await expect(page.locator('#channel-title')).toHaveText('公共频道');
 expect(calls.replies).toEqual([{invite_id:'x',action:'decline'}]);expect(calls.voice).toBe(0);
});

test('ticking partners from any channel joins voice first, then invites them together',async({page})=>{
 const {calls,send}=await setup(page);
 await page.getByRole('button',{name:'拉人进语音',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'拉人进语音'});
 await expect(dialog.getByRole('checkbox')).toHaveCount(3); // everyone online except me, including other channels
 await expect(dialog.locator('li').filter({hasText:'小博'})).toContainText('在「第二频道」');await expect(dialog.locator('li').filter({hasText:'小安'})).toContainText('本频道');
 await expect(dialog.getByRole('checkbox',{name:/小陈/})).toBeDisabled(); // already in voice
 await expect(dialog.getByRole('button',{name:'拉进语音',exact:true})).toBeDisabled();
 await dialog.getByRole('checkbox',{name:/小安/}).check();
 await expect(dialog.getByRole('button',{name:'拉进语音（1）',exact:true})).toBeEnabled();
 await dialog.getByRole('button',{name:'全选',exact:true}).click();
 await expect(dialog.getByRole('checkbox',{name:/小博/})).toBeChecked();
 // A partner who goes offline drops out of the selection.
 send({type:'online',users:online.filter(user=>user.id!=='bob')});
 await expect(dialog.getByRole('button',{name:'拉进语音（1）',exact:true})).toBeVisible();
 send({type:'online',users:online});await dialog.getByRole('checkbox',{name:/小博/}).check();
 await dialog.getByRole('button',{name:'拉进语音（2）',exact:true}).click();
 await expect(dialog).toBeHidden();await expect(page.locator('#notice')).toHaveText('已邀请 小安、小博，等待对方应答…');
 expect(calls.voice).toBe(1);await expect(page.locator('#leave-voice')).toBeVisible();
 expect(calls.invites).toHaveLength(1);expect(calls.invites[0].targets).toEqual(['ann','bob']);expect(calls.invites[0].invite_id).toBeTruthy();
 send({type:'voice_invite_reply',invite_id:calls.invites[0].invite_id,user:users[1],action:'decline'});
 await expect(page.locator('#notice')).toHaveText('小安 拒绝了语音邀请');
 send({type:'voice_invite_reply',invite_id:calls.invites[0].invite_id,user:users[2],action:'timeout'});
 await expect(page.locator('#notice')).toHaveText('小博 未接听语音邀请');
 // Already in voice: a second round invites without joining again.
 await page.getByRole('button',{name:'拉人进语音',exact:true}).click();
 await dialog.getByRole('checkbox',{name:/小安/}).check();await dialog.getByRole('button',{name:'拉进语音（1）',exact:true}).click();
 await expect.poll(()=>calls.invites.length).toBe(2);expect(calls.voice).toBe(1);
});

test('an inviter who cannot join voice sends no invitation',async({page})=>{
 const {calls}=await setup(page,{joinFails:true});
 await page.getByRole('button',{name:'拉人进语音',exact:true}).click();
 const dialog=page.getByRole('dialog',{name:'拉人进语音'});
 await dialog.getByRole('checkbox',{name:/小安/}).check();await dialog.getByRole('button',{name:'拉进语音（1）',exact:true}).click();
 await expect(dialog.locator('#invite-error')).toContainText('需要先加入群语音');
 await expect(dialog).toBeVisible();expect(calls.invites).toEqual([]);
 await page.keyboard.press('Escape');await expect(dialog).toBeHidden();
});

test.describe('phone layout',()=>{
 test.use({viewport:{width:390,height:844}});
 test('inviting and being invited fit a phone screen',async({page})=>{
  const {ring}=await setup(page);
  await expect(page.getByRole('button',{name:'拉人进语音',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'拉人进语音',exact:true}).click();
  await expect(page.getByRole('dialog',{name:'拉人进语音'}).getByRole('checkbox')).toHaveCount(3);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:'test-results/invite-picker-mobile.png'});
  await page.getByRole('button',{name:'取消',exact:true}).click();ring();
  await expect(card(page)).toBeVisible();
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.screenshot({path:'test-results/invite-incoming-mobile.png'});
 });
});
