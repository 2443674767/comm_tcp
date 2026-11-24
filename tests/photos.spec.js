import {test,expect} from '@playwright/test';
import {readFileSync} from 'node:fs';
const png=readFileSync(new URL('./fixtures/photo.png',import.meta.url));
async function enter(page,name){await page.goto('/');await page.getByLabel('你的昵称').fill(name);await page.getByRole('button',{name:'进入聊天室',exact:true}).click();await expect(page.locator('#connection-label')).toHaveText('已连接');}
test('mobile photos preview, cancel, send, persist and open full size',async({browser})=>{
 const cs=await Promise.all([{}, {viewport:{width:390,height:844},isMobile:true,hasTouch:true}].map(o=>browser.newContext(o)));const [a,b]=await Promise.all(cs.map(c=>c.newPage()));
 try{await enter(a,'照片电脑');await enter(b,'照片手机');
 const channel='照片测试-'+Date.now();await a.getByRole('button',{name:'创建频道',exact:true}).click();await a.getByLabel('频道名称').fill(channel);await a.getByRole('button',{name:'确认创建',exact:true}).click();await expect(a.locator('#channel-title')).toHaveText(channel);await expect(b.locator('#channel-select option').filter({hasText:channel})).toHaveCount(1);await b.getByLabel('当前频道').selectOption({label:channel});
 await b.locator('#choose-photo').click({trial:true}); // Browser file chooser is handled by setInputFiles below.
 // Preserve actual user gesture that establishes the target channel.
 const chooser=b.waitForEvent('filechooser');await b.locator('#choose-photo').click();await (await chooser).setFiles({name:'phone.png',mimeType:'image/png',buffer:png});
 const preview=b.getByRole('dialog',{name:'照片预览',exact:true});await expect(preview).toBeVisible();await expect(preview.locator('img')).toHaveJSProperty('naturalWidth',640);await preview.getByRole('button',{name:'取消',exact:true}).click();await expect(preview).not.toBeVisible();expect(await a.locator('.message-photo').count()).toBe(0);
 await expect(b.locator('#photo-camera')).toHaveAttribute('capture','environment');
 const camera=b.waitForEvent('filechooser');await b.locator('#take-photo').click();await (await camera).setFiles({name:'camera.png',mimeType:'image/png',buffer:png});await expect(preview).toBeVisible();await preview.getByRole('button',{name:'发送照片',exact:true}).click();await expect(a.locator('.message-photo img')).toHaveCount(1);await expect(a.locator('.message-photo img')).toHaveJSProperty('naturalWidth',640);
 await b.reload();await expect(b.locator('.message-photo')).toHaveCount(1);await b.locator('.message-photo').click();const viewer=b.getByRole('dialog',{name:'查看照片',exact:true});await expect(viewer).toBeVisible();await expect(viewer.locator('img')).toHaveJSProperty('naturalWidth',640);expect(await b.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await b.screenshot({path:'test-results/photo-mobile.png'});await viewer.getByRole('button',{name:'关闭照片'}).click();
 await b.getByLabel('当前频道').selectOption('public');await expect(b.locator('.message-photo')).toHaveCount(0);
 }finally{await Promise.all(cs.map(c=>c.close()))}
});
test('invalid photo displays a recoverable error without sending',async({page})=>{await enter(page,'照片错误');const chooser=page.waitForEvent('filechooser');await page.locator('#choose-photo').click();await (await chooser).setFiles({name:'bad.jpg',mimeType:'image/jpeg',buffer:Buffer.from('not a picture')});await expect(page.locator('#notice')).toContainText('无法读取照片');await expect(page.getByRole('dialog',{name:'照片预览',exact:true})).not.toBeVisible();});
