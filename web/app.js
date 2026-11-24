import {setupPhotos} from './photos.js?v=video-photo-1';
const $ = id => document.getElementById(id);
const state = { channel:"public", channels:[], voiceChannel:null, user:null, ws:null, retry:null, epoch:0, liveMessages:new Set(), messages:new Map(), firstPage:true, historyCursor:0, older:true, syncing:false, resyncRequested:false, historyRetry:null, pc:null, mic:null, joining:false, voiceEpoch:0, voiceCallID:null, recorder:null, recordStream:null, recordTimer:null, recordStarted:0, recordBlob:null, recordURL:null, recordID:null, recordCancelled:false, recordingBusy:false, sending:false, retryText:null, online:[], voiceMembers:[], inviteSelected:new Set(), inviting:false, incoming:new Map() };
let noticeTimer;
function notice(text) { $('notice').textContent=text; $('notice').hidden=false; clearTimeout(noticeTimer); noticeTimer=setTimeout(()=>$('notice').hidden=true,6500); }
function setConnection(status,label) { $('connection').dataset.state=status; $('connection-label').textContent=label; }
async function api(path, options={}) {
  const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),30000);
  try {
    const response=await fetch(path,{credentials:'same-origin',...options,signal:controller.signal});
    const result=await response.json();
    if(!response.ok) { const error=new Error(result.error||'请求失败，请重试');error.status=response.status;if(response.status===401&&state.user)resetSession();throw error; }
    return result;
  } catch(error) { if(error.name==='AbortError')throw new Error('请求超时，请检查连接后重试');throw error; }
  finally {clearTimeout(timer);}
}
const jsonOptions=(method,body)=>({method,headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
const id=()=>crypto.randomUUID();
function colorFor(value) { return [...value].reduce((sum,c)=>sum+c.charCodeAt(0),0)%5; }
function avatar(name,userID) { const el=document.createElement('div');el.className='avatar';el.textContent=Array.from(name).slice(0,1).join('');el.dataset.color=colorFor(userID);return el; }
// Each tab selects its own channel; all API calls carry an immutable channel.
function channelURL(path,channel=state.channel) {
  if(channel==='public')return path;
  return `${path}${path.includes('?')?'&':'?'}channel_id=${encodeURIComponent(channel)}`;
}
let soundContext;let soundEnabled=true;
try{soundEnabled=localStorage.getItem('lan-chat-sound')!=='off';}catch{}
function soundUI(){
  $('sound-toggle').textContent=`提示音：${soundEnabled?'开':'关'}`;
  $('sound-toggle').setAttribute('aria-label',soundEnabled?'关闭提示音':'开启提示音');
  $('sound-toggle').setAttribute('aria-pressed',String(soundEnabled));
  $('sound-help').textContent=!soundEnabled?'已静音':soundContext?.state==='running'?(speechUnavailable?'中文播报不可用 · 提示音仍有效':'当前频道消息与语音进出'):'点击页面后启用提示音';
}
function unlockSound(){
  if(!soundEnabled||soundContext?.state==='running')return;
  try{
    if(!soundContext){soundContext=new (window.AudioContext||window.webkitAudioContext)();soundContext.onstatechange=soundUI;}
    if(soundContext.state!=='running')soundContext.resume().then(soundUI).catch(()=>{});
    // A silent buffer unlocks playback on browsers that require a gesture.
    const source=soundContext.createBufferSource();source.buffer=soundContext.createBuffer(1,1,soundContext.sampleRate);source.connect(soundContext.destination);source.start();
  }catch{}soundUI();
}
const notificationTones=new Set();
let announcementGeneration=0,announcementRunning=false,announcementQueue=[],cancelSpeech=null,speechUnavailable=false;
function playNotification(kind='message',count=1){
  if(!soundEnabled||soundContext?.state!=='running')return Promise.resolve();
  return new Promise(resolve=>{
    let remaining=count;
    for(let i=0;i<count;i++){
      const oscillator=soundContext.createOscillator(),gain=soundContext.createGain(),start=soundContext.currentTime+i*0.28;
      oscillator.type='sine';oscillator.frequency.setValueAtTime(kind==='join'?880:kind==='leave'?440:660,start);
      gain.gain.setValueAtTime(0,start);gain.gain.linearRampToValueAtTime(0.12,start+0.015);gain.gain.exponentialRampToValueAtTime(0.001,start+0.18);
      oscillator.connect(gain);gain.connect(soundContext.destination);notificationTones.add(oscillator);
      oscillator.onended=()=>{notificationTones.delete(oscillator);oscillator.disconnect();gain.disconnect();if(--remaining===0)resolve();};
      oscillator.start(start);oscillator.stop(start+0.2);
    }
  });
}
function cancelAnnouncements(){
  announcementGeneration++;announcementQueue=[];announcementRunning=false;
  cancelSpeech?.();
  for(const oscillator of notificationTones){try{oscillator.stop();}catch{}}
}
function speakAnnouncement(text,generation){
  const synthesis=window.speechSynthesis;
  if(!synthesis||typeof window.SpeechSynthesisUtterance!=='function'){
    speechUnavailable=true;soundUI();return Promise.resolve();
  }
  return new Promise(resolve=>{
    let finished=false,started=false,utterance=null,timer;
    const finish=(success,stop=false)=>{
      if(finished)return;finished=true;clearTimeout(timer);
      synthesis.removeEventListener?.('voiceschanged',trySpeak);
      if(utterance){utterance.onend=null;utterance.onerror=null;utterance.onstart=null;}
      if(cancelSpeech===cancel)cancelSpeech=null;
      if(stop){try{synthesis.cancel();}catch{}}
      if(success!==null&&generation===announcementGeneration){speechUnavailable=!success;soundUI();}
      resolve();
    };
    const cancel=()=>finish(null,true);
    const trySpeak=()=>{
      if(finished||started)return;
      try{
        // Use a local Chinese voice: nicknames must not be sent to a cloud TTS service.
        const voices=synthesis.getVoices();
        const chinese=voices.filter(voice=>voice.localService&&/^(zh|cmn)([-_]|$)/i.test(voice.lang));
        const voice=chinese.find(voice=>/CN|Hans/i.test(voice.lang))||chinese[0];
        if(!voice){if(voices.length)finish(false);return;}
        started=true;clearTimeout(timer);
        utterance=new window.SpeechSynthesisUtterance(text);utterance.voice=voice;utterance.lang=voice.lang.replaceAll('_','-');
        utterance.rate=1;utterance.pitch=1;utterance.volume=0.9;
        utterance.onend=()=>finish(true);utterance.onerror=()=>finish(false,true);
        // Bound both engine startup and playback so an unavailable engine cannot block the queue.
        utterance.onstart=()=>{clearTimeout(timer);timer=setTimeout(()=>finish(false,true),20000);};
        timer=setTimeout(()=>finish(false,true),4000);
        synthesis.speak(utterance);
      }catch{finish(false,true);}
    };
    cancelSpeech=cancel;
    synthesis.addEventListener?.('voiceschanged',trySpeak);
    timer=setTimeout(()=>finish(false),1500);trySpeak();
  });
}
async function drainAnnouncements(){
  if(announcementRunning)return;
  announcementRunning=true;const generation=announcementGeneration;
  try{
    while(announcementQueue.length&&soundEnabled&&generation===announcementGeneration){
      const {action,nickname}=announcementQueue.shift();
      await speakAnnouncement(`${nickname}${action==='join'?'加入':'退出'}语音`,generation);
      if(generation!==announcementGeneration||!soundEnabled)break;
      await playNotification(action,action==='join'?2:1);
    }
  }finally{if(generation===announcementGeneration)announcementRunning=false;}
}
function announceVoiceEvent(action,nickname){
  if(!['join','leave'].includes(action)||!soundEnabled||soundContext?.state!=='running')return;
  announcementQueue.push({action,nickname});void drainAnnouncements();
}
// An incoming voice invitation rings until it is answered, cancelled or expires.
// The ring follows the sound toggle and waits for the first gesture like other tones.
const ringTones=new Set();let ringTimer=null;const pageTitle=document.title;
function ringOnce(){
  if(!soundEnabled||soundContext?.state!=='running')return;
  for(const [offset,frequency] of [[0,784],[0.2,988],[0.5,784],[0.7,988]]){
    const oscillator=soundContext.createOscillator(),gain=soundContext.createGain(),start=soundContext.currentTime+offset;
    oscillator.type='triangle';oscillator.frequency.setValueAtTime(frequency,start);
    gain.gain.setValueAtTime(0,start);gain.gain.linearRampToValueAtTime(0.22,start+0.02);gain.gain.exponentialRampToValueAtTime(0.001,start+0.18);
    oscillator.connect(gain);gain.connect(soundContext.destination);ringTones.add(oscillator);
    oscillator.onended=()=>{ringTones.delete(oscillator);oscillator.disconnect();gain.disconnect();};
    oscillator.start(start);oscillator.stop(start+0.2);
  }
}
function silenceRing(){for(const oscillator of ringTones){try{oscillator.stop();}catch{}}}
function startRing(){if(ringTimer)return;document.title=`📞 语音邀请 · ${pageTitle}`;ringOnce();ringTimer=setInterval(ringOnce,2000);}
function stopRing(){clearInterval(ringTimer);ringTimer=null;document.title=pageTitle;silenceRing();}
document.addEventListener('pointerdown',unlockSound,{capture:true});document.addEventListener('pointerup',unlockSound,{capture:true});document.addEventListener('click',unlockSound,{capture:true});document.addEventListener('keydown',unlockSound,{capture:true});
$('sound-toggle').onclick=()=>{soundEnabled=!soundEnabled;try{localStorage.setItem('lan-chat-sound',soundEnabled?'on':'off');}catch{}if(soundEnabled)unlockSound();else{cancelAnnouncements();silenceRing();}soundUI();};soundUI();
function showChannels(){
  const options=state.channels.map(channel=>{const option=document.createElement('option');option.value=channel.id;option.textContent=channel.name;return option;});
  $('channel-select').replaceChildren(...options);$('channel-select').value=state.channel;
  $('channel-title').textContent=state.channels.find(c=>c.id===state.channel)?.name||'公共频道';
}
async function refreshChannels(){
  const user=state.user;if(!user)return;
  try{const channels=await api('/api/channels');if(state.user!==user)return;state.channels=[...new Map([...state.channels,...channels].map(c=>[c.id,c])).values()];showChannels();}catch(e){if(state.user===user)notice(e.message);}
}
function clearChannel(){
  photos.clear();
  cancelAnnouncements();closeInvitePicker();state.epoch++;clearTimeout(state.retry);clearTimeout(state.historyRetry);state.retry=null;
  if(state.ws){state.ws.onclose=null;state.ws.close();state.ws=null;}
  discardRecording();state.recorder=null;state.recordingBusy=false;state.messages.clear();state.liveMessages.clear();state.firstPage=true;state.historyCursor=0;state.syncing=false;state.resyncRequested=false;state.older=true;state.retryText=null;state.sending=false;
  $('messages').replaceChildren();$('message-input').value='';$('char-count').textContent='0 / 4000';$('send-button').disabled=false;$('load-older').hidden=true;$('load-older').disabled=false;$('empty-state').hidden=false;$('new-messages').hidden=true;showUsers([]);showVoiceMembers([]);
}
function switchChannel(channel){
  if(channel===state.channel)return;
  leaveVoice();clearChannel();state.channel=channel;
  try{sessionStorage.setItem('lan-chat-channel',channel);}catch{}
  showChannels();connect();
}
$('channel-select').onchange=()=>switchChannel($('channel-select').value);
$('new-channel').onclick=()=>{$('channel-form').hidden=!$('channel-form').hidden;if(!$('channel-form').hidden)$('channel-name').focus();};
$('cancel-channel').onclick=()=>{$('channel-form').hidden=true;};
$('channel-form').onsubmit=async event=>{
  event.preventDefault();const user=state.user;$('create-channel').disabled=true;$('channel-error').textContent='';
  try{const channel=await api('/api/channels',jsonOptions('POST',{name:$('channel-name').value.trim()}));if(state.user!==user)return;await refreshChannels();if(state.user!==user)return;if(!state.channels.some(c=>c.id===channel.id))state.channels.push(channel);switchChannel(channel.id);$('channel-form').hidden=true;$('channel-name').value='';}catch(e){if(state.user===user)$('channel-error').textContent=e.message;}finally{$('create-channel').disabled=false;}
};
function resetSession() {
  leaveVoice(false);clearIncoming();state.online=[];state.user=null;clearChannel();state.channel='public';state.channels=[];
  $('channel-form').hidden=true;$('app').hidden=true;$('login-screen').hidden=false;
}
async function enter(user) {
  state.user=user;state.epoch++;const epoch=state.epoch;$('login-screen').hidden=true;$('app').hidden=false;$('my-name').textContent=user.nickname;$('my-avatar').replaceWith(Object.assign(avatar(user.nickname,user.id),{id:'my-avatar'}));
  await refreshChannels();if(epoch!==state.epoch)return;
  let remembered;try{remembered=sessionStorage.getItem('lan-chat-channel');}catch{}
  state.channel=state.channels.some(c=>c.id===remembered)?remembered:'public';showChannels();connect();
  if(new URLSearchParams(location.search).get('diagnose')==='1')runVoiceDiagnostic();
}
$('login-form').addEventListener('submit',async event=>{
  event.preventDefault();$('login-submit').disabled=true;$('login-error').textContent='';
  try{await enter(await api('/api/login',jsonOptions('POST',{nickname:$('nickname').value.trim()})));}catch(e){$('login-error').textContent=e.message;}finally{$('login-submit').disabled=false;}
});
$('logout').onclick=async()=>{try{await api('/api/logout',jsonOptions('POST',{}));resetSession();}catch(e){notice(e.message);}};
function connect() {
  if(!state.user)return;clearTimeout(state.retry);const epoch=state.epoch;
  setConnection('connecting','连接中');const ws=new WebSocket(`${location.protocol==='https:'?'wss:':'ws:'}//${location.host}${channelURL('/ws')}`);state.ws=ws;
  ws.onmessage=event=>{
    if(epoch!==state.epoch||state.ws!==ws)return;
    try{
      const data=JSON.parse(event.data);
      if(data.type==='ready'){setConnection('online','已连接');syncHistory(epoch);refreshChannels();}
      else if(data.type==='message'){const m=data.message;if(!m.channel_id||m.channel_id===state.channel){if(!state.liveMessages.has(m.id)&&m.session_id!==state.user?.id)playNotification();state.liveMessages.add(m.id);addMessage(m);}}
      else if(data.type==='presence')showUsers(data.users);
      else if(data.type==='online'){state.online=data.users;renderInvitePicker();}
      else if(data.type==='voice')showVoiceMembers(data.members);
      else if(data.type==='voice_event'){announceVoiceEvent(data.action,data.member.nickname);notice(`${data.member.nickname}${data.action==='join'?'加入':'退出'}了群语音`);}
      else if(data.type==='voice_invite')receiveInvite(data);
      else if(data.type==='voice_invite_end')endInvite(data);
      else if(data.type==='voice_invite_reply')notice(`${data.user.nickname} ${inviteReplies[data.action]||'没有加入语音'}`);
      else if(data.type==='channels_changed')refreshChannels();
    }catch(e){notice('消息解析失败，请刷新页面重试');}
  };
  ws.onclose=()=>{
    if(epoch!==state.epoch)return;cancelAnnouncements();setConnection('offline','已断开 · 重连中');
    if(state.pc||state.joining){leaveVoice(false);notice('网络中断，已退出群语音；恢复连接后可重新加入');}
    state.retry=setTimeout(()=>{api('/api/me').then(user=>{if(epoch!==state.epoch)return;if(user.id!==state.user?.id){resetSession();enter(user);}else connect();}).catch(()=>{if(epoch===state.epoch&&state.user)state.retry=setTimeout(connect,3000);});},1500);
  };
  ws.onerror=()=>ws.close();
}
function showUsers(users) {
  $('online-count').textContent=users.length;$('online-users').replaceChildren();
  for(const user of users){const li=document.createElement('li');li.append(avatar(user.nickname,user.id));const name=document.createElement('span');name.className='online-name';name.textContent=user.nickname;li.append(name);if(user.id===state.user?.id){const you=document.createElement('small');you.textContent='你';li.append(you);}const dot=document.createElement('i');dot.className='online-dot';li.append(dot);$('online-users').append(li);}
}
function showVoiceMembers(members) {
  state.voiceMembers=members;renderInvitePicker();renderVideoTiles();
  $('voice-count').textContent=`${members.length} / 5`;$('voice-members').replaceChildren();
  for(const member of members){const chip=document.createElement('span');chip.className='voice-chip';chip.textContent=`${member.nickname}${member.id===state.user?.id?' · 你':''}`;$('voice-members').append(chip);}
}
const nearBottom=()=>{const el=$('messages-scroll');return el.scrollHeight-el.scrollTop-el.clientHeight<90;};
function bottom(){const el=$('messages-scroll');el.scrollTop=el.scrollHeight;$('new-messages').hidden=true;}
$('new-messages').onclick=bottom;$('messages-scroll').onscroll=()=>{if(nearBottom())$('new-messages').hidden=true;};
function messageNode(m) {
  const own=m.session_id===state.user?.id;const row=document.createElement('article');row.className=`message${own?' own':''}`;row.dataset.id=m.id;row.append(avatar(m.nickname,m.session_id));
  const content=document.createElement('div');content.className='message-content';const meta=document.createElement('div');meta.className='message-meta';const name=document.createElement('strong');name.textContent=m.nickname+(own?' · 我':'');const time=document.createElement('time');time.dateTime=m.created_at;time.textContent=new Date(m.created_at).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false});meta.append(name,time);
  const bubble=document.createElement('div');bubble.className='message-bubble';
  if(m.kind==='audio'){const caption=document.createElement('div');caption.className='audio-caption';caption.textContent=`语音消息 · ${Math.max(1,Math.round(m.duration))} 秒`;const audio=document.createElement('audio');audio.controls=true;audio.preload='none';audio.src=channelURL(`/media/${encodeURIComponent(m.media)}`,m.channel_id||'public');audio.setAttribute('aria-label',`${m.nickname}的语音消息`);audio.addEventListener('error',()=>notice('录音无法播放，请检查网络或尝试更新浏览器'));bubble.append(caption,audio);}
  else if(m.kind==='photo')photos.render(m,bubble);
  else{const text=document.createElement('div');text.className='message-text';text.textContent=m.text;bubble.append(text);}
  content.append(meta,bubble);row.append(content);return row;
}
function addMessage(m,quiet=false) {
  if(!state.user||(m.channel_id&&m.channel_id!==state.channel)||state.messages.has(m.id))return;
  const follow=nearBottom();state.messages.set(m.id,m);const row=messageNode(m);const siblings=$('messages').children;
  let before=null;for(const child of siblings){if(Number(child.dataset.id)>m.id){before=child;break;}}
  $('messages').insertBefore(row,before);$('empty-state').hidden=true;
  if(!quiet){if(follow||m.session_id===state.user.id)bottom();else $('new-messages').hidden=false;}
}
async function syncHistory(epoch) {
  if(state.syncing){state.resyncRequested=true;return;}clearTimeout(state.historyRetry);state.syncing=true;state.resyncRequested=false;
  try{
    if(state.firstPage){const page=await api(channelURL('/api/messages'));if(epoch!==state.epoch)return;for(const m of page)addMessage(m,true);state.older=page.length===50;state.historyCursor=page.at(-1)?.id||0;state.firstPage=false;bottom();}
    else{let cursor=state.historyCursor;let page;do{page=await api(channelURL(`/api/messages?after=${cursor}`));if(epoch!==state.epoch)return;for(const m of page)addMessage(m);if(page.length){cursor=page.at(-1).id;state.historyCursor=cursor;}}while(page.length===50);}
    $('empty-state').hidden=state.messages.size>0;$('load-older').hidden=!state.older;
  }catch(e){if(epoch!==state.epoch)return;notice(`历史消息加载失败：${e.message}`);if(epoch===state.epoch)state.historyRetry=setTimeout(()=>syncHistory(epoch),3000);}
  finally{if(epoch===state.epoch){state.syncing=false;if(state.resyncRequested)queueMicrotask(()=>syncHistory(epoch));}}
}
$('load-older').onclick=async()=>{
  const epoch=state.epoch;const scroll=$('messages-scroll');const height=scroll.scrollHeight;$('load-older').disabled=true;
  try{const oldest=Math.min(...state.messages.keys());const page=await api(channelURL(`/api/messages?before=${oldest}`));if(epoch!==state.epoch)return;for(const m of page)addMessage(m,true);state.older=page.length===50;$('load-older').hidden=!state.older;scroll.scrollTop+=scroll.scrollHeight-height;}catch(e){if(epoch===state.epoch)notice(e.message);}finally{if(epoch===state.epoch)$('load-older').disabled=false;}
};
$('message-input').oninput=()=>{$('char-count').textContent=`${Array.from($('message-input').value).length} / 4000`;};
$('message-input').onkeydown=event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing&&matchMedia('(min-width:641px)').matches){event.preventDefault();$('message-form').requestSubmit();}};
$('message-form').onsubmit=async event=>{
  event.preventDefault();const text=$('message-input').value.trim();if(!text||state.sending)return;
  const epoch=state.epoch;state.sending=true;$('send-button').disabled=true;
  const clientID=state.retryText?.text===text?state.retryText.clientID:id();state.retryText={text,clientID};
  try{const m=await api(channelURL('/api/messages'),jsonOptions('POST',{text,client_id:clientID}));if(epoch!==state.epoch)return;addMessage(m);if($('message-input').value.trim()===text)$('message-input').value='';$('message-input').oninput();state.retryText=null;bottom();}catch(e){if(epoch===state.epoch)notice(`发送失败，内容已保留：${e.message}`);}finally{if(epoch===state.epoch){state.sending=false;$('send-button').disabled=false;}}
};

function microphoneError(error) {
  if(!window.isSecureContext)return '麦克风需要 HTTPS；请使用加密地址，并安装、信任局域网证书。';
  if(error.name==='NotAllowedError'||error.name==='SecurityError')return '麦克风权限未开启，请在浏览器设置中允许本站使用麦克风后重试。';
  if(error.name==='NotFoundError')return '没有找到麦克风，请先连接麦克风。';
  if(error.name==='NotReadableError')return '麦克风可能正被其他应用占用，请关闭占用后重试。';
  return error.message||'无法使用麦克风，请检查设备和浏览器权限。';
}
async function getMicrophone() {
  if(!navigator.mediaDevices?.getUserMedia)throw new Error('麦克风不可用：请使用 HTTPS 并信任证书，或更新浏览器。');
  return navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true,noiseSuppression:true,autoGainControl:true},video:false});
}
function releaseStream(stream){stream?.getTracks().forEach(track=>track.stop());}
function clock(seconds){return `${String(Math.floor(seconds/60)).padStart(2,'0')}:${String(Math.floor(seconds%60)).padStart(2,'0')}`;}
function discardRecording() {
  state.recordCancelled=true;clearInterval(state.recordTimer);
  if(state.recorder?.state==='recording')state.recorder.stop();releaseStream(state.recordStream);state.recordStream=null;state.recordBlob=null;state.recordID=null;
  if(state.recordURL)URL.revokeObjectURL(state.recordURL);state.recordURL=null;$('record-preview').pause();$('record-preview').removeAttribute('src');$('record-panel').hidden=true;$('record-button').disabled=false;$('send-record').disabled=false;$('discard-record').disabled=false;
}
$('discard-record').onclick=discardRecording;
$('record-button').onclick=async()=>{
  if(state.pc||state.joining){notice('请先退出群语音，再录制语音消息');return;}
  if(state.recordingBusy)return;if(state.recordBlob){notice('请先发送或取消当前录音');return;}
  const epoch=state.epoch;state.recordingBusy=true;$('record-button').disabled=true;
  try{
    if(typeof MediaRecorder==='undefined')throw new Error('此浏览器不支持网页录音，请更新浏览器');
    const stream=await getMicrophone();if(epoch!==state.epoch){releaseStream(stream);return;}
    state.recordStream=stream;const mime=['audio/webm;codecs=opus','audio/ogg;codecs=opus','audio/mp4'].find(t=>MediaRecorder.isTypeSupported(t));if(!mime)throw new Error('浏览器没有支持的录音格式，请使用新版 Chrome、Safari 或 Firefox');
    const recorder=new MediaRecorder(stream,{mimeType:mime,audioBitsPerSecond:64000});state.recorder=recorder;state.recordCancelled=false;const chunks=[];state.recordStarted=performance.now();let duration=0;
    recorder.ondataavailable=event=>{if(event.data.size)chunks.push(event.data);};
    recorder.onerror=()=>{if(epoch===state.epoch){discardRecording();notice('录音中断，请重试');}};
    recorder.onstop=()=>{
      releaseStream(stream);if(epoch!==state.epoch)return;clearInterval(state.recordTimer);state.recordStream=null;
      if(state.recordCancelled)return;
      duration=Math.min(120,(performance.now()-state.recordStarted)/1000);
      state.recordBlob=new Blob(chunks,{type:recorder.mimeType});state.recordBlob.duration=duration;state.recordID=id();
      if(!state.recordBlob.size||state.recordBlob.size>10*1024*1024){discardRecording();notice('录音为空或超过 10 MiB，请重新录制');return;}
      state.recordURL=URL.createObjectURL(state.recordBlob);$('record-preview').src=state.recordURL;$('record-preview').hidden=false;$('record-label').textContent='录音完成 · 可以试听';$('record-time').textContent=clock(duration);$('stop-record').hidden=true;$('send-record').hidden=false;$('send-record').disabled=false;
    };
    $('record-panel').hidden=false;$('record-preview').hidden=true;$('stop-record').hidden=false;$('send-record').hidden=true;$('record-label').textContent='● 正在录音';$('record-time').textContent='00:00';recorder.start(250);
    state.recordTimer=setInterval(()=>{const seconds=(performance.now()-state.recordStarted)/1000;$('record-time').textContent=clock(seconds);if(seconds>=119.5&&recorder.state==='recording')recorder.stop();},200);
  }catch(e){if(epoch!==state.epoch)return;releaseStream(state.recordStream);state.recordStream=null;$('record-button').disabled=false;notice(microphoneError(e));}
  finally{if(epoch===state.epoch)state.recordingBusy=false;}
};
$('stop-record').onclick=()=>{if(state.recorder?.state==='recording')state.recorder.stop();};
$('send-record').onclick=async()=>{
  const blob=state.recordBlob;if(!blob)return;const epoch=state.epoch;$('send-record').disabled=true;$('discard-record').disabled=true;
  const data=new FormData();const ext=blob.type.includes('mp4')?'m4a':blob.type.includes('ogg')?'ogg':'webm';data.append('audio',blob,`recording.${ext}`);data.append('duration',String(blob.duration));data.append('client_id',state.recordID);
  try{const m=await api(channelURL('/api/audio'),{method:'POST',body:data});if(epoch!==state.epoch)return;addMessage(m);discardRecording();bottom();}catch(e){if(epoch===state.epoch)notice(`录音发送失败，可以重试：${e.message}`);}finally{if(epoch===state.epoch){$('send-record').disabled=false;$('discard-record').disabled=false;}}
};


// Video publication is opt-in. The negotiated send slot stays alive while the
// camera is released, so receivers and camera toggles need no renegotiation.
let cameraStream=null,videoSender=null,cameraBusy=false,cameraGeneration=0,cameraFacing='user';
const remoteVideos=new Map();
function cameraUI(){
 $('camera-toggle').textContent=cameraStream?'关闭摄像头':'开启摄像头';
 $('camera-toggle').setAttribute('aria-pressed',String(!!cameraStream));
 $('camera-toggle').disabled=cameraBusy;
 $('camera-switch').disabled=cameraBusy||!cameraStream;
}
function stopCamera(){
 cameraGeneration++;releaseStream(cameraStream);cameraStream=null;cameraBusy=false;cameraFacing='user';cameraUI();
}
function renderVideoTiles(){
 const grid=$('video-grid');if(!grid)return;
 grid.hidden=!state.pc;
 const members=state.pc?state.voiceMembers:[];
 const ids=new Set(members.map(m=>m.id));
 for(const tile of [...grid.children])if(!ids.has(tile.dataset.user))tile.remove();
 for(const member of members){
  let tile=[...grid.children].find(t=>t.dataset.user===member.id);
  if(!tile){tile=document.createElement('div');tile.className='video-tile';tile.dataset.user=member.id;
   const video=document.createElement('video');video.autoplay=true;video.playsInline=true;video.muted=true;
   const name=document.createElement('span');name.className='video-name';const placeholder=document.createElement('span');placeholder.className='video-placeholder';placeholder.textContent='摄像头未开启';
   tile.append(video,placeholder,name);grid.append(tile);
   // A stable receiver track remains live when its publisher releases a camera.
   // Only display newly presented frames, never a departed member's frozen frame.
   let presented=0;
   const onFrame=()=>{if(!tile.isConnected)return;if(++presented>1){tile.lastVideoFrame=performance.now();tile.classList.add('has-video');}video.requestVideoFrameCallback?.(onFrame);};
   if(video.requestVideoFrameCallback)video.requestVideoFrameCallback(onFrame);
   else video.ontimeupdate=()=>{if(video.readyState>=2){tile.lastVideoFrame=performance.now();tile.classList.add('has-video');}};

  }
  const own=member.id===state.user?.id,stream=own?cameraStream:remoteVideos.get(member.slot);
  const video=tile.querySelector('video');
  if(video.srcObject!==(stream||null)){video.srcObject=stream||null;if(stream)video.play().catch(()=>{});}
  tile.querySelector('.video-name').textContent=member.nickname+(own?' · 你':'');
  tile.classList.toggle('local-video',own&&cameraFacing==='user');
  tile.classList.toggle('has-video',!!stream&&(own||performance.now()-(tile.lastVideoFrame||-Infinity)<1800));
 }
}
setInterval(()=>{if(state.pc)for(const tile of $('video-grid').children){if(tile.dataset.user!==state.user?.id&&performance.now()-(tile.lastVideoFrame||-Infinity)>1800)tile.classList.remove('has-video');}},500);
async function setCamera(switching=false){
 if(!state.pc||!videoSender||cameraBusy)return;
 if(cameraStream&&!switching){
  const epoch=state.voiceEpoch,sender=videoSender;
  cameraBusy=true;cameraUI();
  try{await sender.replaceTrack(null);if(epoch!==state.voiceEpoch)return;stopCamera();renderVideoTiles();}
  catch{if(epoch!==state.voiceEpoch)return;cameraBusy=false;cameraUI();notice('摄像头关闭失败，请重试');}return;
 }
 const epoch=state.voiceEpoch,generation=++cameraGeneration,sender=videoSender;
 const facing=switching?(cameraFacing==='user'?'environment':'user'):cameraFacing;
 cameraBusy=true;cameraUI();let stream;
 try{
  // Mobile devices often require releasing the active camera before switching.
  if(switching){await sender.replaceTrack(null);if(epoch!==state.voiceEpoch||generation!==cameraGeneration)return;releaseStream(cameraStream);cameraStream=null;renderVideoTiles();}
  stream=await navigator.mediaDevices.getUserMedia({audio:false,video:{facingMode:switching?{exact:facing}:{ideal:facing},width:{ideal:640,max:1280},height:{ideal:480,max:720},frameRate:{ideal:20,max:24}}});
  if(epoch!==state.voiceEpoch||generation!==cameraGeneration){releaseStream(stream);return;}
  const track=stream.getVideoTracks()[0];await sender.replaceTrack(track);
  if(epoch!==state.voiceEpoch||generation!==cameraGeneration){releaseStream(stream);return;}
  cameraStream=stream;cameraFacing=facing;
  track.onended=()=>{if(cameraStream===stream){stopCamera();renderVideoTiles();notice('摄像头已断开，语音通话继续');}};
  const parameters=sender.getParameters();
  if(parameters.encodings?.length){parameters.encodings[0].maxBitrate=700000;await sender.setParameters(parameters).catch(()=>{});}
 }catch(e){releaseStream(stream);if(epoch===state.voiceEpoch&&generation===cameraGeneration)notice(switching?'摄像头切换失败，可重新开启；语音通话继续':'无法开启摄像头，请检查权限或设备；语音通话继续');}
 finally{if(epoch===state.voiceEpoch&&generation===cameraGeneration){cameraBusy=false;cameraUI();renderVideoTiles();}}
}
$('camera-toggle').onclick=()=>setCamera();
$('camera-switch').onclick=()=>setCamera(true);
function voiceUI(joined) { $('camera-controls').hidden=!joined;renderVideoTiles();$('join-voice').hidden=joined;$('voice-controls').hidden=!joined;$('join-voice').disabled=false;$('join-voice').textContent='加入群语音';if(!joined){$('voice-status').textContent='随时加入，一起聊聊';$('enable-audio').hidden=true;}}
function leaveVoice(notifyServer=true) {
  state.voiceEpoch++;stopCamera();videoSender=null;remoteVideos.clear();state.joining=false;const callID=state.voiceCallID,channel=state.voiceChannel;state.voiceCallID=null;state.voiceChannel=null;const pc=state.pc;state.pc=null;if(pc){pc.onconnectionstatechange=null;pc.close();}releaseStream(state.mic);state.mic=null;$('remote-audio').replaceChildren();voiceUI(false);
  if(notifyServer&&state.user&&callID)api(channelURL(`/api/voice?call_id=${encodeURIComponent(callID)}`,channel),{method:'DELETE'}).catch(e=>notice(e.message));
}
$('leave-voice').onclick=()=>leaveVoice();
$('mute-voice').onclick=()=>{const track=state.mic?.getAudioTracks()[0];if(!track)return;track.enabled=!track.enabled;$('mute-voice').textContent=track.enabled?'静音':'取消静音';$('mute-voice').setAttribute('aria-label',track.enabled?'静音麦克风':'开启麦克风');};
$('enable-audio').onclick=async()=>{try{await Promise.all([...$('remote-audio').children].map(el=>el.play()));$('enable-audio').hidden=true;}catch{notice('请确认设备音量和浏览器播放权限');}};
async function gather(pc) {
  if(pc.iceGatheringState==='complete')return;
  return new Promise((resolve,reject)=>{
    const cleanup=()=>{clearTimeout(timer);pc.removeEventListener('icegatheringstatechange',check);};
    const check=()=>{if(pc.iceGatheringState==='complete'){cleanup();resolve();}};
    const timer=setTimeout(()=>{cleanup();reject(new Error('语音网络地址收集超时，请重试'));},8000);pc.addEventListener('icegatheringstatechange',check);check();
  });
}
function reportVoiceSupport(stage){
  const typeOf=value=>['undefined','function','object'].includes(typeof value)?typeof value:'object';
  // Only browser capabilities are reported; no nickname, message, or credential.
  const report={stage,build:'voice-diagnostic-2',secure:window.isSecureContext,
    rtc_global:typeof RTCPeerConnection,rtc_window:typeOf(window.RTCPeerConnection),
    rtc_webkit:typeOf(window.webkitRTCPeerConnection),rtc_moz:typeOf(window.mozRTCPeerConnection),
    transceiver:typeof window.RTCPeerConnection?.prototype?.addTransceiver==='function',
    microphone:typeof navigator.mediaDevices?.getUserMedia==='function',audio_state:soundContext?.state||'none'};
  return fetch('/api/voice-support',{...jsonOptions('POST',report),credentials:'same-origin'}).then(response=>response.ok).catch(()=>false);
}
function runVoiceDiagnostic(){
  const epoch=state.epoch;
  $('voice-diagnostic').hidden=false;$('voice-diagnostic').textContent='检测版本 D2 · 正在发送记录';
  $('voice-status').textContent=voiceSupportError()||'当前浏览器提供群语音接口，可以尝试加入。';
  reportVoiceSupport('before-join').then(ok=>{if(epoch===state.epoch)$('voice-diagnostic').textContent=ok?'检测版本 D2 · 记录已送达':'检测版本 D2 · 记录发送失败，请检查连接';});
}
function voiceSupportError(){
  if(!window.isSecureContext)return '请先使用 HTTPS 并信任本机证书，再加入群语音；仅跳过证书警告可能无法使用麦克风。';
  if(typeof window.RTCPeerConnection!=='function')return '当前浏览器未提供群语音功能（WebRTC）。请复制地址到系统浏览器打开：iPhone 使用 Safari，安卓使用新版 Chrome；不要使用应用内置浏览器。';
  if(typeof window.RTCPeerConnection.prototype?.addTransceiver!=='function')return '当前浏览器的群语音功能不完整，请更新浏览器或改用新版 Safari / Chrome。';
  if(!navigator.mediaDevices?.getUserMedia)return '当前页面无法访问麦克风。请确认 HTTPS 证书已受信任，并在支持麦克风的系统浏览器中打开。';
  return '';
}
// Resolves true once this tab is in the channel's voice room.
async function joinVoice(){
  if(state.pc||state.joining)return false;
  reportVoiceSupport('before-join');
  const supportError=voiceSupportError();
  if(supportError){$('voice-status').textContent=supportError;notice(supportError);return;}
  if(state.recorder?.state==='recording'||state.recordingBusy){notice('请先结束录音，再加入群语音');return;}
  if(state.ws?.readyState!==WebSocket.OPEN){notice('聊天连接尚未恢复，请稍后加入');return;}
  const epoch=++state.voiceEpoch;const callID=id(),channel=state.channel;state.voiceChannel=channel;state.voiceCallID=callID;state.joining=true;$('join-voice').disabled=true;$('join-voice').textContent='正在连接…';let pc;
  try{
    const mic=await getMicrophone();if(epoch!==state.voiceEpoch){releaseStream(mic);return;}state.mic=mic;reportVoiceSupport('after-microphone');
    // Create playback elements inside the user gesture's media session, then
    // expose an explicit playback button if the browser still blocks autoplay.
    pc=new window.RTCPeerConnection({iceServers:[]});state.pc=pc;
    pc.addTransceiver(mic.getAudioTracks()[0],{direction:'sendonly',streams:[mic]});
    for(let i=0;i<4;i++)pc.addTransceiver('audio',{direction:'recvonly'});
    const videoTransceiver=pc.addTransceiver('video',{direction:'sendonly'});
    videoSender=videoTransceiver.sender;
    const videoTransceivers=[videoTransceiver];
    for(let i=0;i<4;i++)videoTransceivers.push(pc.addTransceiver('video',{direction:'recvonly'}));
    const codecs=window.RTCRtpReceiver?.getCapabilities?.('video')?.codecs.filter(c=>c.mimeType.toLowerCase()==='video/vp8');
    if(codecs?.length)for(const t of videoTransceivers)t.setCodecPreferences?.(codecs);
    pc.ontrack=event=>{if(epoch!==state.voiceEpoch)return;
      if(event.track.kind==='video'){
        const slot=Number(event.streams[0]?.id.match(/^slot-(\d+)$/)?.[1]);
        if(Number.isInteger(slot)){remoteVideos.set(slot,new MediaStream([event.track]));renderVideoTiles();}
        return;
      }
      const audio=document.createElement('audio');audio.autoplay=true;audio.playsInline=true;audio.srcObject=new MediaStream([event.track]);$('remote-audio').append(audio);audio.play().catch(()=>{if(epoch===state.voiceEpoch)$('enable-audio').hidden=false;});};
    pc.onconnectionstatechange=()=>{
      if(epoch!==state.voiceEpoch)return;
      if(pc.connectionState==='connected'){$('voice-status').textContent='通话中';}
      else if(['failed','disconnected','closed'].includes(pc.connectionState)){leaveVoice();notice('语音连接中断，请检查网络和语音端口后重新加入');}
    };
    await pc.setLocalDescription(await pc.createOffer());await gather(pc);if(epoch!==state.voiceEpoch)return;
    const answer=await api(channelURL('/api/voice',channel),jsonOptions('POST',{call_id:callID,offer:pc.localDescription}));
    if(epoch!==state.voiceEpoch){api(channelURL(`/api/voice?call_id=${encodeURIComponent(callID)}`,channel),{method:'DELETE'}).catch(()=>{});return;}
    await pc.setRemoteDescription(answer);if(epoch!==state.voiceEpoch)return;state.joining=false;voiceUI(true);$('mute-voice').textContent='静音';$('mute-voice').setAttribute('aria-label','静音麦克风');$('voice-status').textContent=pc.connectionState==='connected'?'通话中':'正在建立语音连接…';
    setTimeout(()=>{if(epoch===state.voiceEpoch&&pc.connectionState!=='connected'){leaveVoice();notice('语音未能连通，请检查 18087/UDP 或 18085/TCP 是否可达');}},15000);
    return true;
  }catch(e){if(epoch===state.voiceEpoch){reportVoiceSupport('join-error');leaveVoice(true);notice(microphoneError(e));}}
  return false;
}
$('join-voice').onclick=()=>{joinVoice();};

// Pulling people into voice: tick anyone online, in any channel, then ring them.
const channelName=channel=>state.channels.find(c=>c.id===channel)?.name||'其他频道';
function renderInvitePicker(){
  if($('invite-overlay').hidden)return;
  const inVoice=new Set(state.voiceMembers.map(member=>member.id));
  const others=state.online.filter(user=>user.id!==state.user?.id);
  const available=new Set(others.filter(user=>!inVoice.has(user.id)).map(user=>user.id));
  for(const id of [...state.inviteSelected])if(!available.has(id))state.inviteSelected.delete(id);
  $('invite-list').replaceChildren(...others.map(user=>{
    const li=document.createElement('li'),label=document.createElement('label'),box=document.createElement('input'),name=document.createElement('span');
    box.type='checkbox';box.value=user.id;box.checked=state.inviteSelected.has(user.id);box.disabled=!available.has(user.id)||state.inviting;
    name.className='online-name';name.textContent=user.nickname;label.append(box,avatar(user.nickname,user.id),name);
    const note=document.createElement('small');note.textContent=!available.has(user.id)?'已在语音中':user.channel_id===state.channel?'本频道':`在「${channelName(user.channel_id)}」`;label.append(note);
    if(!available.has(user.id))label.className='unavailable';
    li.append(label);return li;
  }));
  $('invite-empty').hidden=others.length>0;$('invite-all').hidden=available.size===0;
  $('invite-all').textContent=available.size&&state.inviteSelected.size===available.size?'取消全选':'全选';$('invite-all').disabled=state.inviting;
  const count=state.inviteSelected.size;$('invite-send').disabled=state.inviting||count===0;
  if(!state.inviting)$('invite-send').textContent=count?`拉进语音（${count}）`:'拉进语音';
}
function closeInvitePicker(){$('invite-overlay').hidden=true;state.inviteSelected.clear();}
$('invite-open').onclick=()=>{
  if(state.ws?.readyState!==WebSocket.OPEN){notice('聊天连接尚未恢复，请稍后再试');return;}
  state.inviteSelected.clear();$('invite-error').textContent='';$('invite-overlay').hidden=false;renderInvitePicker();
};
$('invite-cancel').onclick=closeInvitePicker;
$('invite-overlay').onclick=event=>{if(event.target===$('invite-overlay'))closeInvitePicker();};
document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!$('invite-overlay').hidden)closeInvitePicker();});
$('invite-list').onchange=event=>{if(event.target.checked)state.inviteSelected.add(event.target.value);else state.inviteSelected.delete(event.target.value);renderInvitePicker();};
$('invite-all').onclick=()=>{
  const boxes=[...$('invite-list').querySelectorAll('input:not(:disabled)')];const all=boxes.every(box=>box.checked);
  for(const box of boxes){if(all)state.inviteSelected.delete(box.value);else state.inviteSelected.add(box.value);}
  renderInvitePicker();
};
$('invite-send').onclick=async()=>{
  const targets=[...state.inviteSelected];if(!targets.length||state.inviting)return;
  const epoch=state.epoch,channel=state.channel;state.inviting=true;$('invite-error').textContent='';$('invite-send').textContent=state.pc?'正在邀请…':'正在加入语音…';renderInvitePicker();
  try{
    // The inviter must be in the call first; this click is the gesture that unlocks the microphone.
    if(!state.pc){
      if(state.joining)throw new Error('正在加入群语音，请稍后再邀请');
      if(!await joinVoice()){if(epoch===state.epoch)$('invite-error').textContent='需要先加入群语音才能邀请，请按提示处理后重试';return;}
    }
    if(epoch!==state.epoch)return;
    $('invite-send').textContent='正在邀请…';
    const result=await api(channelURL('/api/voice-invite',channel),jsonOptions('POST',{invite_id:id(),targets}));
    if(epoch!==state.epoch)return;
    closeInvitePicker();
    notice(result.invited.length?`已邀请 ${result.invited.map(user=>user.nickname).join('、')}，等待对方应答…`:'所选伙伴已在语音中、正在响铃或已离线');
  }catch(e){if(epoch===state.epoch)$('invite-error').textContent=e.message;}
  finally{state.inviting=false;renderInvitePicker();}
};

// Being pulled into voice: a card with accept / decline that rings until settled.
const inviteReplies={accept:'接受了语音邀请',decline:'拒绝了语音邀请',timeout:'未接听语音邀请',offline:'已离线，邀请未送达',failed:'未能加入语音'};
function dismissIncoming(inviteID){
  const invite=state.incoming.get(inviteID);if(!invite)return null;
  clearInterval(invite.timer);invite.card.remove();state.incoming.delete(inviteID);if(!state.incoming.size)stopRing();return invite;
}
function clearIncoming(){for(const inviteID of [...state.incoming.keys()])dismissIncoming(inviteID);}
function replyInvite(inviteID,action,channel){api(channelURL('/api/voice-invite/reply',channel),jsonOptions('POST',{invite_id:inviteID,action})).catch(()=>{});}
function receiveInvite(data){
  if(!state.user||data.from.id===state.user.id||state.incoming.has(data.invite_id))return;
  const inviteID=data.invite_id,nickname=data.from.nickname,channel=data.channel_id||'public',card=document.createElement('section');
  if(!state.channels.some(c=>c.id===channel))refreshChannels().then(()=>{text.textContent=text.textContent.replace('其他频道',channelName(channel));});
  card.className='incoming-invite';card.setAttribute('role','alertdialog');card.setAttribute('aria-label',`${nickname} 邀请你加入群语音`);
  const head=document.createElement('div'),copy=document.createElement('div'),name=document.createElement('strong'),text=document.createElement('span');
  head.className='incoming-head';name.textContent=nickname;text.textContent=`邀请你加入「${channelName(channel)}」群语音${channel===state.channel?'':'，接受后自动切换到该频道'}`;copy.append(name,text);head.append(avatar(nickname,data.from.id),copy);
  const countdown=document.createElement('small');countdown.className='incoming-timer';
  const actions=document.createElement('div'),decline=document.createElement('button'),accept=document.createElement('button');
  actions.className='incoming-actions';decline.className='danger';decline.textContent='拒绝';accept.className='primary';accept.textContent='接受并加入';
  decline.onclick=()=>{if(dismissIncoming(inviteID))replyInvite(inviteID,'decline',channel);};
  accept.onclick=()=>acceptInvite(inviteID);
  actions.append(decline,accept);card.append(head,countdown,actions);
  const deadline=performance.now()+(data.expires_in||30)*1000;
  const tick=()=>{
    const left=Math.ceil((deadline-performance.now())/1000);
    // The server ends the invitation; this only covers a lost end event.
    if(left<-2){if(dismissIncoming(inviteID))notice(`未接听 ${nickname} 的语音邀请`);return;}
    countdown.textContent=`${Math.max(0,left)} 秒内未应答将自动结束`;
  };
  tick();state.incoming.set(inviteID,{card,nickname,channel,timer:setInterval(tick,500)});$('incoming-invites').append(card);startRing();
}
function endInvite(data){
  const invite=dismissIncoming(data.invite_id);if(!invite)return;
  if(data.reason==='cancelled')notice(`${invite.nickname} 已取消语音邀请`);
  else if(data.reason==='timeout')notice(`未接听 ${invite.nickname} 的语音邀请`);
}
function waitConnected(epoch,timeout=8000){
  return new Promise(resolve=>{
    const started=performance.now();
    const check=()=>{
      if(epoch!==state.epoch)return resolve(false);
      if(state.ws?.readyState===WebSocket.OPEN&&$('connection').dataset.state==='online')return resolve(true);
      if(performance.now()-started>timeout)return resolve(false);
      setTimeout(check,100);
    };check();
  });
}
async function acceptInvite(inviteID){
  const invite=state.incoming.get(inviteID);if(!invite)return;
  if(state.recorder?.state==='recording'||state.recordingBusy){notice('请先结束或取消录音，再接受语音邀请');return;}
  const channel=invite.channel;dismissIncoming(inviteID);
  // The call lives in the inviter's channel: move there first, then join.
  if(channel!==state.channel){
    if(!state.channels.some(c=>c.id===channel))await refreshChannels();
    if(!state.user)return;
    if(!state.channels.some(c=>c.id===channel)){notice('邀请所在的频道已不存在');replyInvite(inviteID,'failed',channel);return;}
    switchChannel(channel);
    if(!await waitConnected(state.epoch)){if(state.channel===channel){notice('已切换频道，但连接尚未就绪，请稍后手动加入群语音');replyInvite(inviteID,'failed',channel);}return;}
  }
  // Joining the room is the acceptance: the server tells the inviter once we are in.
  if(state.pc||state.joining)return;
  if(!await joinVoice())replyInvite(inviteID,'failed',channel);
}

window.addEventListener('pagehide',()=>{photos.clear();cancelAnnouncements();clearIncoming();discardRecording();if(state.pc||state.joining){const callID=state.voiceCallID,channel=state.voiceChannel;leaveVoice(false);if(callID)fetch(channelURL(`/api/voice?call_id=${encodeURIComponent(callID)}`,channel),{method:'DELETE',keepalive:true}).catch(()=>{});}state.ws?.close();});
window.addEventListener('pageshow',event=>{if(event.persisted&&state.user&&state.ws?.readyState===WebSocket.CLOSED)connect();});
const photos=setupPhotos({state,api,channelURL,notice,addMessage,id});
api('/api/me').then(enter).catch(e=>{if(e.status!==401)notice('暂时无法连接服务器，请检查网络后登录');});
