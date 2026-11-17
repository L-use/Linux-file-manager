import {language,t} from './i18n.js';

export function progressPercent(done,total) {
  const ratio=total>0?Math.min(1,Math.max(0,done/total)):0;
  return new Intl.NumberFormat(language==='zh'?'zh-CN':'en',{
    style:'percent',minimumFractionDigits:2,maximumFractionDigits:2,
  }).format(Math.floor(ratio*10000)/10000);
}

const progressResolution=10000;
const activeProgress=new WeakMap();

export function setProgress(bar,done=0,total=1) {
  const fraction=total>0?Math.min(1,Math.max(0,done/total)):0;
  bar.max=progressResolution;
  bar.value=Math.floor(fraction*progressResolution);
}

export function startProgress(host,label='loading',compact=false) {
  const element=document.createElement('div');
  element.className='operation-progress'+(compact?' compact':'');
  const caption=document.createElement('span');
  const bar=document.createElement('progress');
  element.append(caption,bar);
  let completed=false;
  function update(key,done=0,total=1) {
    if (completed) return;
    caption.dataset.i18n=bar.dataset.label=key;
    caption.textContent=t(key);
    bar.setAttribute('aria-label',caption.textContent);
    setProgress(bar,done,total);
  }
  function complete() {
    completed=true;
    setProgress(bar,1,1);
  }
  function finish() {
    if (completed && element.isConnected) setTimeout(()=>element.remove(),180);
    else element.remove();
  }
  update(label);
  host.append(element);
  const progress={element,update,complete,finish};
  activeProgress.set(element,progress);
  return progress;
}

export function completeReady(host) {
  const indicator=host?.querySelector(':scope > .operation-progress, :scope > .empty > .operation-progress');
  const progress=activeProgress.get(indicator);
  progress?.complete();
  return progress;
}

export function replaceReady(host,...children) {
  const progress=completeReady(host);
  if (progress) {
    children.push(progress.element);
  }
  host.replaceChildren(...children);
  progress?.finish();
}

export async function withProgress(host,label,task) {
  const progress=startProgress(host,label);
  try {
    const result=await task(progress);
    if (result!==false) progress.complete();
    return result;
  } finally {progress.finish();}
}
