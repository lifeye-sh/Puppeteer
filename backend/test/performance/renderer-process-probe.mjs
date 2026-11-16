// Executed only by the isolated acceptance runner. No process arguments or secrets.
import {readdir,readFile} from 'node:fs/promises';
async function snapshot(){
 let processes=0,openFDs=0,unreadableFDProcesses=0;
 const zombieIdentities=[];
 for(const pid of await readdir('/proc')){
  if(!/^\d+$/.test(pid)||Number(pid)===process.pid)continue;
  const status=await readFile(`/proc/${pid}/status`,'utf8').catch(()=>'');if(!status)continue;
  processes++;
  if(/^State:\s+Z/m.test(status)){
   const stat=await readFile(`/proc/${pid}/stat`,'utf8').catch(()=>'');
   if(stat)zombieIdentities.push(pid+':'+stat.slice(stat.lastIndexOf(')')+2).split(' ')[19]);
  }
  const fds=await readdir(`/proc/${pid}/fd`).catch(()=>null);
  if(fds)openFDs+=fds.length;else unreadableFDProcesses++;
 }
 return {processes,zombies:zombieIdentities.length,zombieIdentities,openFDs,unreadableFDProcesses};
}
const first=await snapshot(),started=Date.now();
let remaining=first.zombieIdentities;
while(remaining.length&&Date.now()-started<2000){
 await new Promise(r=>setTimeout(r,100));
 const current=new Set((await snapshot()).zombieIdentities);
 remaining=remaining.filter(id=>current.has(id));
}
console.log(JSON.stringify({...first,persistentZombies:remaining.length,persistentZombieIdentities:remaining,recheckMS:Date.now()-started}));
