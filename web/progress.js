import {t} from './i18n.js';

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
      amount.textContent=unit==='count'?`${bar.value} / ${total}`:`${Math.floor(100*bar.value/total)}%`;
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
