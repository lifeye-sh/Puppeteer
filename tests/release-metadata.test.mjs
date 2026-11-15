import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8');
const version=read('VERSION').trim();
const source=JSON.parse(read('deploy/production/source-release.json'));
test('published installer and upgrade defaults match the release manifest',()=>{
 assert.equal(source.version,version);
 if(!source.containerImagesPublished)return;
 const release=JSON.parse(read(`deploy/production/dockerhub-${version}.json`));
 const install=read('scripts/install.sh'),upgrade=read('scripts/upgrade.sh');
 assert.match(install,new RegExp(`^version=${version}$`,'m'));
 assert.match(upgrade,new RegExp(`^version=${version}$`,'m'));
 assert.match(release.sourceCheckout,/^[a-f0-9]{40}$/);
 assert.match(install,new RegExp(`^revision=${release.sourceCheckout}$`,'m'));
 assert.equal(release.schemaVersion,source.schemaVersion);
 assert.equal(release.capabilitiesRevision,source.capabilitiesRevision);
 assert.deepEqual(release.images.map(x=>x.component).sort(),['graphics','renderer','server','web']);
 for(const image of release.images){
  assert.equal(image.reference,`cloners/puppeteer-${image.component}:${version}`);
  assert.equal(image.pinnedReference,`cloners/puppeteer-${image.component}@${image.digest}`);
  assert.match(image.digest,/^sha256:[a-f0-9]{64}$/);
  assert.ok(install.includes(`PUPPETEER_${image.component.toUpperCase()}_IMAGE=${image.pinnedReference}\n`));
 }
});
