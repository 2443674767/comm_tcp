// Photo composition stays scoped to the session/channel that opened the picker.
export function setupPhotos({state,api,channelURL,notice,addMessage,id}) {
 const sheet=document.createElement('link');sheet.rel='stylesheet';sheet.href='/photos.css?v=video-photo-1';document.head.append(sheet);
 const controls=document.createElement('div');controls.className='photo-tools';
 controls.innerHTML='<button type="button" class="text-button" id="choose-photo">▧ 照片</button><button type="button" class="text-button" id="take-photo">◉ 拍照</button><input id="photo-file" type="file" accept="image/*" hidden><input id="photo-camera" type="file" accept="image/*" capture="environment" hidden>';
 document.querySelector('.composer-wrap').prepend(controls);
 const dialog=document.createElement('dialog');dialog.className='photo-dialog';dialog.setAttribute('aria-label','照片预览');
 dialog.innerHTML='<h2>发送照片</h2><img alt="待发送照片" id="photo-preview"><p id="photo-info" role="status"></p><div class="photo-actions"><button type="button" class="secondary" id="cancel-photo">取消</button><button type="button" class="primary" id="send-photo">发送照片</button></div>';
 document.body.append(dialog);
 const viewer=document.createElement('dialog');viewer.className='photo-viewer';viewer.setAttribute('aria-label','查看照片');viewer.innerHTML='<button type="button" class="secondary" aria-label="关闭照片">关闭 ×</button><img alt="聊天照片大图">';document.body.append(viewer);
 const query=id=>dialog.querySelector('#'+id);let generation=0,pending=null,previewURL=null,busy=false,selection=null;
 const closeViewer=()=>{viewer.close();viewer.querySelector('img').removeAttribute('src')};viewer.querySelector('button').onclick=closeViewer;viewer.addEventListener('close',()=>viewer.querySelector('img').removeAttribute('src'));
 function clear(){generation++;pending=null;selection=null;busy=false;dialog.close();closeViewer();if(previewURL)URL.revokeObjectURL(previewURL);previewURL=null;query('photo-preview').removeAttribute('src');query('send-photo').disabled=false;query('cancel-photo').disabled=false;controls.querySelectorAll('input').forEach(i=>i.value='');}
 dialog.addEventListener('cancel',e=>{e.preventDefault();if(!busy)clear()});query('cancel-photo').onclick=clear;
 function pick(input){if(!state.user)return;selection={epoch:state.epoch,channel:state.channel};input.value='';input.click();}
 controls.querySelector('#choose-photo').onclick=()=>pick(controls.querySelector('#photo-file'));
 controls.querySelector('#take-photo').onclick=()=>pick(controls.querySelector('#photo-camera'));
 async function prepare(file,context){
  if(!file||!context||context.epoch!==state.epoch)return;
  const token=++generation;const sourceURL=URL.createObjectURL(file);let image;
  try{
   if(file.size>25*1024*1024)throw new Error('原照片最大 25 MiB，请先缩小照片');
   image=new Image();image.src=sourceURL;await image.decode();
   if(token!==generation||context.epoch!==state.epoch)return;
   if(image.naturalWidth*image.naturalHeight>80e6)throw new Error('照片像素过大，请选择普通拍照模式');
   const scale=Math.min(1,1920/Math.max(image.naturalWidth,image.naturalHeight));const canvas=document.createElement('canvas');canvas.width=Math.max(1,Math.round(image.naturalWidth*scale));canvas.height=Math.max(1,Math.round(image.naturalHeight*scale));
   const ctx=canvas.getContext('2d');ctx.fillStyle='#fff';ctx.fillRect(0,0,canvas.width,canvas.height);ctx.drawImage(image,0,0,canvas.width,canvas.height);
   const blob=await new Promise(resolve=>canvas.toBlob(resolve,'image/jpeg',0.85));canvas.width=canvas.height=1;
   if(token!==generation||context.epoch!==state.epoch)return;
   if(!blob||blob.size>10*1024*1024)throw new Error('照片压缩失败，请选择较小的照片');
   if(previewURL)URL.revokeObjectURL(previewURL);previewURL=URL.createObjectURL(blob);pending={blob,clientID:id(),...context};query('photo-preview').src=previewURL;query('photo-info').textContent=`${Math.ceil(blob.size/1024)} KB · 发送到当前频道`;dialog.showModal();
  }catch(e){if(token===generation)notice(e.message.includes('照片')?e.message:'无法读取照片，请选择 JPEG、PNG 或浏览器支持的图片');}
  finally{URL.revokeObjectURL(sourceURL);if(image)image.src='';}
 }
 for(const input of controls.querySelectorAll('input'))input.onchange=()=>prepare(input.files?.[0],selection);
 query('send-photo').onclick=async()=>{
  if(!pending||busy)return;const item=pending,token=generation;busy=true;query('send-photo').disabled=true;query('cancel-photo').disabled=true;query('photo-info').textContent='正在发送…';
  const form=new FormData();form.append('photo',item.blob,'photo.jpg');form.append('client_id',item.clientID);
  try{const message=await api(channelURL('/api/photos',item.channel),{method:'POST',body:form});if(token!==generation||item.epoch!==state.epoch)return;addMessage(message);clear();}
  catch(e){if(token===generation)query('photo-info').textContent=e.message+'，可重试';}
  finally{if(token===generation){busy=false;query('send-photo').disabled=false;query('cancel-photo').disabled=false;}}
 };
 return {clear,render(m,bubble){
  const button=document.createElement('button');button.type='button';button.className='message-photo';button.setAttribute('aria-label',`查看${m.nickname}的照片`);
  const image=document.createElement('img');image.alt=`${m.nickname}的照片`;image.loading='lazy';image.src=channelURL(`/media/${encodeURIComponent(m.media)}`,m.channel_id||'public');
  image.onerror=()=>{image.alt='照片加载失败，点击重试或查看';};button.append(image);button.onclick=()=>{const full=viewer.querySelector('img');full.src=image.src;viewer.showModal()};bubble.append(button);
 }};
}
