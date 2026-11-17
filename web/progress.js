import {language,t} from './i18n.js';

export function progressPercent(done,total) {
  const ratio=total>0?Math.min(1,Math.max(0,done/total)):0;
  return new Intl.NumberFormat(language==='zh'?'zh-CN':'en',{
    style:'percent',minimumFractionDigits:2,maximumFractionDigits:2,
  }).format(Math.floor(ratio*10000)/10000);
}

function createProgress(label='loading', compact=false) {
  const element=document.createElement('div');
  element.className='operation-progress'+(compact?' compact':'');
  const caption=document.createElement('span');
  const bar=document.createElement('progress');
  const amount=document.createElement('span');
  element.append(caption,bar,amount);
  function update(key,done=0,total=0,unit='percent') {
    caption.dataset.i18n=bar.dataset.label=key;
    caption.textContent=t(key);
    bar.setAttribute('aria-label',caption.textContent);
    if (total>0) {
      bar.max=total;
      bar.value=Math.min(done,total);
      const percent=progressPercent(bar.value,total);
      amount.textContent=unit==='count'?`${percent} · ${bar.value} / ${total}`:percent;
    } else {
      bar.removeAttribute('value');
      amount.textContent='';
    }
  }
  update(label);
  return {element,update,finish:()=>element.remove()};
}

export function startProgress(host,label,compact=false) {
  const progress=createProgress(label,compact);
  host.append(progress.element);
  return progress;
}

export async function withProgress(host,label,task) {
  const progress=startProgress(host,label);
  try {return await task(progress);}
  finally {progress.finish();}
}
