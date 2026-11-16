// Synchronous transitions fence admission before the host stops the container.
export class Lifecycle {
 constructor(now=Date.now){this.now=now;this.lastUse=now();this.draining=false;}
 touch(){this.lastUse=this.now();}
 resume(){this.draining=false;this.touch();}
 drain(busy){
  if(busy||this.now()-this.lastUse<300000)return false;
  this.draining=true;return true;
 }
}
