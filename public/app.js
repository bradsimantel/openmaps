import {initializeRouting} from './routing.js';
const input = document.querySelector('#search');
const results = document.querySelector('#results');
const status = document.querySelector('#search-status');
const details = document.querySelector('#details');
const welcome = document.querySelector('#welcome');
let timer, controller, generation = 0, marker, map, maplibregl;
export {map};
let sessionToken = crypto.randomUUID();
const mode=document.querySelector('#lookup-mode');
let queryMarker, selectedMapResult;
const routing = initializeRouting({getMap:()=>map, getMapLibre:()=>maplibregl, request});

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
 map.on('load',()=>{document.querySelector('#map-status').textContent='Map ready';routing.mapReady();if(selectedMapResult)placeMarker(...selectedMapResult);});
 map.on('click',event=>{if(event.originalEvent.target.closest('.maplibregl-marker, .maplibregl-popup'))return;if(routing.mapClick(event.lngLat))return;mode.value='address';input.value='';updateMode();lookupGeocode({lat:event.lngLat.lat,lng:event.lngLat.lng});});
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
 if(!response.ok)throw new Error(body.error?.message || body.error_message || 'Request failed');
 return body;
}
function changed() {
 clearTimeout(timer);controller?.abort();const current=++generation;
 results.replaceChildren();details.hidden=true;welcome.hidden=Boolean(input.value.trim());
 clearMarkers();
 if(mode.value==='address'){status.textContent='Enter a house number and complete street name, then Find address. For example: 50 Bellevue Ave.';return;}
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
  placeMarker(p.displayName.text,p.location.longitude,p.location.latitude,p.types.includes('political')?12:16);
  status.textContent=`Selected ${p.displayName.text}.`;
 }catch(error){if(error.name!=='AbortError'&&current===generation)status.textContent=error.message;}
}
function clearMarkers(){routing.selection(null);marker?.remove();queryMarker?.remove();marker=queryMarker=null;selectedMapResult=null;}
function placeMarker(label,lng,lat,zoom=16){
 routing.selection({label,lng,lat});
 selectedMapResult=[label,lng,lat,zoom];if(!map||!maplibregl)return;
 marker?.remove();const popup=new maplibregl.Popup({offset:30,focusAfterOpen:false}).setText(label);
 marker=new maplibregl.Marker({color:'#3d6848'}).setLngLat([lng,lat]).setPopup(popup).addTo(map);
 marker.getElement().setAttribute('aria-label',`Map marker: ${label}`);
 marker.togglePopup();map.flyTo({center:[lng,lat],zoom});
}
function updateMode(){
 document.querySelector('#geocode-submit').hidden=mode.value!=='address';
 input.placeholder=mode.value==='address'?'50 Bellevue Ave':'Place, street, or address';
}
mode.addEventListener('change',()=>{updateMode();changed();});
document.querySelector('#lookup-form').addEventListener('submit',event=>{event.preventDefault();if(mode.value==='address')lookupGeocode();});
async function lookupGeocode(point){
 clearTimeout(timer);controller?.abort();const current=++generation;controller=new AbortController();
 clearMarkers();results.replaceChildren();details.hidden=true;welcome.hidden=true;
 status.textContent=point?'Finding a nearby address…':'Looking up address…';
 if(point&&map){const dot=element('div','','query-marker');queryMarker=new maplibregl.Marker({element:dot}).setLngLat([point.lng,point.lat]).addTo(map);queryMarker.getElement().setAttribute('aria-label','Reverse lookup query point');}
 try{
  const query=point?`latlng=${point.lat},${point.lng}`:`address=${encodeURIComponent(input.value)}`;
  const body=await request(`/maps/api/geocode/json?${query}`,{signal:controller.signal});
  if(current!==generation)return;
  if(body.status==='INVALID_REQUEST')throw new Error(body.error_message);
  if(body.status==='ZERO_RESULTS'){
   status.textContent=body.openmaps.outcome==='outside_coverage'?'Outside the Newport preview rectangle. No address selected.':body.openmaps.outcome==='no_nearby_address'?'No supported address point within 100 metres. Coverage may be incomplete.':'No exact address match. Use the complete street name; check context and coverage. No street or locality fallback is used.';return;
  }
  if(body.status!=='OK')throw new Error(body.error_message||'Geocoding unavailable');
  if(body.openmaps.outcome==='ambiguous'){
   status.textContent=`Ambiguous: ${body.results.length} distinct source address points. Units are unknown. Choose a candidate; none is selected automatically.`;
   for(const r of body.results){const li=element('li','');const button=element('button','');button.append(element('strong',r.formatted_address),element('small',`${r.geometry.location.lat.toFixed(6)}, ${r.geometry.location.lng.toFixed(6)} · ${r.place_id}`));button.addEventListener('click',()=>selectGeocode(r,point,true));li.append(button);results.append(li);}
  }else{selectGeocode(body.results[0],point,false);}
 }catch(error){if(error.name!=='AbortError'&&current===generation)status.textContent=error.message;}
}
function selectGeocode(r,point,ambiguous){
 results.replaceChildren();details.replaceChildren();details.hidden=false;welcome.hidden=true;
 details.append(element('div',point?'REVERSE GEOCODING':'FORWARD GEOCODING','eyebrow'),element('h2',r.formatted_address));
 const p=r.geometry.location;
 details.append(element('p',`${p.lat.toFixed(6)}, ${p.lng.toFixed(6)}`,'coords'));
 details.append(element('p','Address match · source address point · APPROXIMATE. Rooftop, entrance, building containment and unit precision are unknown.'));
 if(point)details.append(element('p',`${r.openmaps.distance_meters.toFixed(2)} metres from click (latitude ${point.lat.toFixed(6)}, longitude ${point.lng.toFixed(6)}). Limit: 100 metres; straight-line distance. Blue dot: click. Green marker: address.`));
 if(r.partial_match)details.append(element('p',`Partial context match: ${r.openmaps.context_note}.`));
 if(ambiguous)details.append(element('p','You chose one of multiple distinct candidates; this choice does not resolve the missing source precision.'));
 details.append(element('p',`Public ID: ${r.place_id}`,'coords'));
 for(const a of r.openmaps.attributions||[])details.append(element('p',a.provider));
 placeMarker(r.formatted_address,p.lng,p.lat);
 status.textContent='Selected source address point. No street or locality fallback.';
 details.scrollIntoView({block:'nearest'});
}
input.addEventListener('input',changed);
input.addEventListener('keydown',event=>{if(event.key==='ArrowDown'){results.querySelector('button')?.focus();event.preventDefault();}if(event.key==='Escape'){input.value='';changed();}});
results.addEventListener('keydown',event=>{
 const buttons=[...results.querySelectorAll('button')];const i=buttons.indexOf(document.activeElement);
 if(event.key==='ArrowDown'){buttons[(i+1)%buttons.length]?.focus();event.preventDefault();}
 if(event.key==='ArrowUp'){if(i<=0)input.focus();else buttons[i-1].focus();event.preventDefault();}
 if(event.key==='Escape')input.focus();
});
document.querySelectorAll('[data-query]').forEach(button=>button.addEventListener('click',()=>{mode.value='places';updateMode();input.value=button.dataset.query;input.focus();changed();}));
