import {open} from 'node:fs/promises';
// Keep request ownership with the HTTP handler so a rejected body can receive
// a useful 413 response instead of pipeline() destroying its socket first.
export async function receiveArchive(request,path,maxBytes=150*1024*1024){
 const file=await open(path,'wx',0o600);let size=0;
 try{
  for await(const chunk of request.iterator({destroyOnReturn:false})){
   size+=chunk.length;if(size>maxBytes)throw Error('input_limit');
   await file.writeFile(chunk);
  }
 }finally{await file.close();}
 return size;
}
