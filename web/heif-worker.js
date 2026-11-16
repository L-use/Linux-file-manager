import initLibheif from '/vendor/libheif-js/runtime/libheif-bundle.mjs';

self.onmessage=async ({data:buffer})=>{
  try {
    const libheif=await initLibheif();
    const images=new libheif.HeifDecoder().decode(new Uint8Array(buffer));
    const primary=images[0];
    if (!primary) throw new Error('No image');
    const width=primary.get_width(),height=primary.get_height();
    if (width<1 || height<1 || width*height>50_000_000) {
      self.postMessage({error:'docLimit'});
      return;
    }
    const {data}=await new Promise((resolve,reject)=>{
      primary.display({data:new Uint8ClampedArray(width*height*4),width,height},result=>{
        if (result) resolve(result);
        else reject(new Error('Decode failed'));
      });
    });
    const pixels=data instanceof Uint8ClampedArray?data:new Uint8ClampedArray(data);
    if (typeof createImageBitmap==='function') {
      try {
        const bitmap=await createImageBitmap(new ImageData(pixels,width,height));
        self.postMessage({width,height,bitmap},[bitmap]);
        return;
      } catch {
        // Older browser workers can still send decoded pixels to a 2D canvas.
      }
    }
    self.postMessage({width,height,data:pixels},[pixels.buffer]);
  } catch {
    self.postMessage({error:'mediaError'});
  }
};
