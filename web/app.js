const input = document.querySelector('#search');
const results = document.querySelector('#results');
const status = document.querySelector('#search-status');
const details = document.querySelector('#details');
const welcome = document.querySelector('#welcome');
let timer, controller, generation = 0, marker, map, maplibregl;
export {map};
let sessionToken = crypto.randomUUID();

async function initializeMap() {
try {
 const [libre, pmtiles, basemaps] = await Promise.all([
  import('https://esm.sh/maplibre-gl@5.0.1?target=es2022'),
  import('https://esm.sh/pmtiles@4.2.1?target=es2022&deps=fflate@0.8.2'),
  import('https://esm.sh/@protomaps/basemaps@5.7.2?target=es2022'),
 ]);
 maplibregl = libre.default;
 const protocol = new pmtiles.Protocol();
 maplibregl.addProtocol('pmtiles', protocol.tile);
 map = new maplibregl.Map({
  container: 'map', center: [-71.312,41.49], zoom: 14,
  style: {version:8,
   glyphs:'https://protomaps.github.io/basemaps-assets/fonts/{fontstack}/{range}.pbf',
   sprite:'https://protomaps.github.io/basemaps-assets/sprites/v4/light',
   sources:{protomaps:{type:'vector',url:`pmtiles://${location.origin}/tiles/newport.pmtiles`,attribution:'<a href="https://www.openstreetmap.org/copyright">© OpenStreetMap contributors</a> · <a href="https://protomaps.com">Protomaps</a>'}},
   layers:basemaps.layers('protomaps',basemaps.namedFlavor('light'),{lang:'en'})}
 });
 map.addControl(new maplibregl.NavigationControl(),'top-right');
 map.on('load',()=>{document.querySelector('#map-status').textContent='Map ready';});
 map.on('error',()=>{document.querySelector('#map-status').textContent='Some map tiles could not load';});
} catch (error) {
 document.querySelector('#map-status').textContent='Map unavailable. Search still works.';
 console.error(error);
}
}
initializeMap();
function element(tag,text,className) { const el=document.createElement(tag);el.textContent=text;if(className)el.className=className;return el; }
function kind(types) {return types.includes('street_address')?'Address':types.includes('route')?'Street':types.includes('political')?'Area':'Place';}
async function request(url,options={}) {
 const response=await fetch(url,options);const body=await response.json();
 if(!response.ok)throw new Error(body.error?.message || 'Request failed');
 return body;
}
function changed() {
 clearTimeout(timer);controller?.abort();const current=++generation;
 results.replaceChildren();details.hidden=true;welcome.hidden=Boolean(input.value.trim());
 if(!input.value.trim()){status.textContent='Search downtown Newport and its nearby streets.';return;}
 status.textContent='Searching…';
 timer=setTimeout(()=>search(current),180);
}
async function search(current) {
 controller=new AbortController();
 try {
  const body=await request('/v1/places:autocomplete',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({input:input.value,sessionToken}),signal:controller.signal});
  if(current!==generation)return;
  results.replaceChildren();
  for(const suggestion of body.suggestions){
   const p=suggestion.placePrediction;const li=document.createElement('li');const button=document.createElement('button');
   button.append(element('span',kind(p.types),'kind'),element('strong',p.structuredFormat.mainText.text));
   if(p.structuredFormat.secondaryText)button.append(element('small',p.structuredFormat.secondaryText.text));
   button.addEventListener('click',()=>select(p));li.append(button);results.append(li);
  }
  status.textContent=body.suggestions.length?`${body.suggestions.length} suggestions. Choose a result to see it on the map.`:'No matches in this region. Try a shorter name or street.';
 }catch(error){if(error.name!=='AbortError'&&current===generation)status.textContent=error.message;}
}
async function select(prediction) {
 clearTimeout(timer);controller?.abort();const current=++generation;controller=new AbortController();
 status.textContent='Loading place…';
 try {
  const p=await request(`/v1/${prediction.place}?sessionToken=${encodeURIComponent(sessionToken)}`,{headers:{'X-Goog-FieldMask':'id,displayName,formattedAddress,location,types,websiteUri,attributions'},signal:controller.signal});
  if(current!==generation)return;
  sessionToken=crypto.randomUUID();results.replaceChildren();details.replaceChildren();details.hidden=false;welcome.hidden=true;
  details.append(element('div',kind(p.types),'eyebrow'),element('h2',p.displayName.text));
  if(p.formattedAddress)details.append(element('p',p.formattedAddress));
  details.append(element('p',`${p.location.latitude.toFixed(6)}, ${p.location.longitude.toFixed(6)}`,'coords'));
  const precision=p.types.includes('route')?'Representative point on a street segment.':p.types.includes('political')?'Area label point; no boundary shown.':p.types.includes('street_address')?'Source address point; entrance and unit precision are unknown.':'Source place location; entrance precision is unknown.';
  details.append(element('p',precision));
  if(p.websiteUri){const a=element('a','Visit website ↗','website');const url=new URL(p.websiteUri);if(['https:','http:'].includes(url.protocol)){a.href=url.href;a.target='_blank';a.rel='noopener noreferrer';details.append(a);}}
  if(map){
   marker?.remove();const popup=new maplibregl.Popup({offset:30}).setText(p.displayName.text);
   marker=new maplibregl.Marker({color:'#3d6848'}).setLngLat([p.location.longitude,p.location.latitude]).setPopup(popup).addTo(map);
   marker.getElement().setAttribute('aria-label',`Map marker: ${p.displayName.text}`);
   marker.togglePopup();map.flyTo({center:[p.location.longitude,p.location.latitude],zoom:p.types.includes('political')?12:16});
  }
  status.textContent=`Selected ${p.displayName.text}.`;
 }catch(error){if(error.name!=='AbortError'&&current===generation)status.textContent=error.message;}
}
input.addEventListener('input',changed);
input.addEventListener('keydown',event=>{if(event.key==='ArrowDown'){results.querySelector('button')?.focus();event.preventDefault();}if(event.key==='Escape'){input.value='';changed();}});
results.addEventListener('keydown',event=>{
 const buttons=[...results.querySelectorAll('button')];const i=buttons.indexOf(document.activeElement);
 if(event.key==='ArrowDown'){buttons[(i+1)%buttons.length]?.focus();event.preventDefault();}
 if(event.key==='ArrowUp'){if(i<=0)input.focus();else buttons[i-1].focus();event.preventDefault();}
 if(event.key==='Escape')input.focus();
});
document.querySelectorAll('[data-query]').forEach(button=>button.addEventListener('click',()=>{input.value=button.dataset.query;input.focus();changed();}));
