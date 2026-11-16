const profiles={
 economy:{width:960,height:540,fps:10,crf:28,jpeg:.8,nativeWidth:960,nativeHeight:540,nativeCRF:28},
 balanced:{width:1280,height:720,fps:20,crf:25,jpeg:.85,nativeWidth:1920,nativeHeight:1080,nativeCRF:23},
 sharp:{width:1920,height:1080,fps:20,crf:22,jpeg:.9,nativeWidth:1920,nativeHeight:1080,nativeCRF:20},
};
export function quality(name='balanced'){
 if(!Object.hasOwn(profiles,name))throw Error('invalid_renderer_quality');
 return {...profiles[name],name};
}
