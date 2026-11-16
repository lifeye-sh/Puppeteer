import {mkdir,readdir,statfs,stat} from 'node:fs/promises';
import {join,resolve} from 'node:path';
const reservation=(process.env.NT_MP4_SCRATCH_DIRECTORY?768:128)*1024*1024;
export const scratchRoot=resolve(process.env.NT_MP4_SCRATCH_DIRECTORY||'/tmp');
const quota=Number(process.env.NT_MP4_SCRATCH_QUOTA_MIB||2048)*1024*1024;
if(!Number.isSafeInteger(quota)||quota<reservation||quota>64*1024**3)throw Error('invalid_scratch_quota');
async function usage(path){
 let bytes=0;
 const entries=await readdir(path,{withFileTypes:true}).catch(e=>{if(e.code==='ENOENT')return [];throw e;});
 for(const entry of entries){
  if(entry.isSymbolicLink())throw Error('unsafe_scratch_entry');
  const child=join(path,entry.name);
  bytes+=entry.isDirectory()?await usage(child):await fileSize(child);
 }
 return bytes;
}
async function fileSize(path){return stat(path).then(s=>s.size).catch(e=>{if(e.code==='ENOENT')return 0;throw e;});}
export async function initializeScratch(){
 await mkdir(scratchRoot,{recursive:true,mode:0o700});
 // Unclaimed files survive restarts and count against admission capacity.
}
export async function admitScratch(){
 const fs=await statfs(scratchRoot);
 let used=0;
 for(const entry of await readdir(scratchRoot,{withFileTypes:true}))if(process.env.NT_MP4_SCRATCH_DIRECTORY||entry.name.startsWith('mp4-job-')){
  if(entry.isSymbolicLink()||!entry.isDirectory()&&!entry.isFile())throw Error('unsafe_scratch_entry');
  const path=join(scratchRoot,entry.name);used+=entry.isDirectory()?await usage(path):await fileSize(path);
 }
 if(used+reservation>quota||fs.bavail*fs.bsize<reservation){const e=Error('temporary_storage_full');e.code='ENOSPC';throw e;}
}
