// Routing receives coordinates only after a user chooses a lookup candidate.
// It never resolves text, collapses ambiguous addresses or moves their identities.
export function initializeRouting({getMap,getMapLibre,request}) {
 const originLabel=document.querySelector('#route-origin'), destinationLabel=document.querySelector('#route-destination');
 const status=document.querySelector('#route-status'), submit=document.querySelector('#route-submit');
 const useOrigin=document.querySelector('#route-use-origin'),useDestination=document.querySelector('#route-use-destination');
 const mapAction=document.querySelector('#map-action');
 let selection=null,origin=null,destination=null,controller,generation=0,busy=false,available=null,geometry=null,snaps=null;
 const markers={};
 const empty=()=>({type:'FeatureCollection',features:[]});
 function draw(){
  const map=getMap(),lib=getMapLibre();if(!map||!lib)return;
  if(!map.getSource('driving-route')){if(!map.isStyleLoaded())return;map.addSource('driving-route',{type:'geojson',data:empty()});map.addLayer({id:'driving-route-casing',type:'line',source:'driving-route',paint:{'line-color':'#fff','line-width':9},layout:{'line-cap':'round','line-join':'round'}});map.addLayer({id:'driving-route-line',type:'line',source:'driving-route',paint:{'line-color':'#285ca0','line-width':5},layout:{'line-cap':'round','line-join':'round'}});}
  if(!map.getSource('route-gaps')){
   map.addSource('route-gaps',{type:'geojson',data:empty()});
   map.addLayer({id:'route-gaps-line',type:'line',source:'route-gaps',paint:{'line-color':'#9b4d16','line-width':3,'line-dasharray':[2,2]}});
  }
  const gaps=empty();
  if(snaps)for(const key of ['origin','destination']){const requested=key==='origin'?origin:destination;if(requested)gaps.features.push({type:'Feature',properties:{},geometry:{type:'LineString',coordinates:[[requested.lng,requested.lat],snaps[key].point]}});}
  map.getSource('route-gaps').setData(gaps);
  for(const key of ['origin-road','destination-road']){markers[key]?.remove();delete markers[key];}
  if(snaps)for(const key of ['origin','destination']){
   const el=document.createElement('div');el.className=`route-marker snapped ${key}`;const label=document.createElement('span');label.textContent=key==='origin'?'A′':'B′';el.appendChild(label);
   el.title=`Snapped ${key}: ${snaps[key].point[1].toFixed(6)}, ${snaps[key].point[0].toFixed(6)}`;el.setAttribute('aria-label',el.title);
   markers[`${key}-road`]=new lib.Marker({element:el}).setLngLat(snaps[key].point).addTo(map);
  }
  map.getSource('driving-route').setData(geometry?{type:'Feature',properties:{},geometry}:empty());
  for(const [key,point] of Object.entries({origin,destination})){
   markers[key]?.remove();delete markers[key];if(!point)continue;
   const el=document.createElement('div');el.className=`route-marker ${key}`;el.textContent=key==='origin'?'A':'B';el.setAttribute('aria-label',`Requested route ${key}: ${point.label}`);
   markers[key]=new lib.Marker({element:el}).setLngLat([point.lng,point.lat]).addTo(map);
   markers[key].getElement().setAttribute('aria-label',`Requested route ${key}: ${point.label}`);
  }
 }
 function update(){originLabel.textContent=origin?`A · ${origin.label} (${origin.lat.toFixed(6)}, ${origin.lng.toFixed(6)})`:'A · Origin not selected';destinationLabel.textContent=destination?`B · ${destination.label} (${destination.lat.toFixed(6)}, ${destination.lng.toFixed(6)})`:'B · Destination not selected';useOrigin.disabled=useDestination.disabled=!selection;submit.disabled=!origin||!destination||busy||available===false;submit.textContent=busy?'Calculating…':'Calculate driving route';}
 function invalidate(message='Endpoints changed. Calculate a new route.') {controller?.abort();generation++;busy=false;geometry=null;snaps=null;status.textContent=available===false?'Routing unavailable in this snapshot. Load a routing candidate to calculate routes.':message;draw();update();}
 function setEndpoint(key,point){if(key==='origin')origin={...point};else destination={...point};invalidate();}
 async function health(){try{const h=await request('/healthz');available=h.routing_available===true;if(!available)invalidate('Routing unavailable in this snapshot. Load a routing candidate to calculate routes.');else if(!geometry&&!busy)status.textContent='Choose both endpoints, then calculate a driving route.';update();}catch{available=null;update();}}
 useOrigin.addEventListener('click',()=>{if(selection)setEndpoint('origin',selection)});
 useDestination.addEventListener('click',()=>{if(selection)setEndpoint('destination',selection)});
 mapAction.addEventListener('change',()=>{if(mapAction.value!=='address')invalidate(`Click the map to set the route ${mapAction.value}.`)});
 document.querySelector('#route-clear').addEventListener('click',()=>{origin=destination=null;mapAction.value='address';invalidate('Route cleared. Choose an origin and destination.');});
 submit.addEventListener('click',async()=>{
  if(!origin||!destination)return;invalidate('Calculating driving route…');busy=true;update();const current=generation;controller=new AbortController();
  const waypoint=p=>({location:{latLng:{latitude:p.lat,longitude:p.lng}}});
  try{
   const body=await request('/directions/v2:computeRoutes',{method:'POST',headers:{'Content-Type':'application/json','X-Goog-FieldMask':'routes.distanceMeters,routes.polyline.geoJsonLinestring'},body:JSON.stringify({origin:waypoint(origin),destination:waypoint(destination),travelMode:'DRIVE',routingPreference:'TRAFFIC_UNAWARE',polylineEncoding:'GEO_JSON_LINESTRING',polylineQuality:'HIGH_QUALITY'}),signal:controller.signal});
   if(current!==generation)return;
   snaps=body.openmaps?.origin&&body.openmaps?.destination?{origin:body.openmaps.origin,destination:body.openmaps.destination}:null;
   const snapText=snaps?['origin','destination'].map(key=>`${key==='origin'?'A′ origin':'B′ destination'} road point ${snaps[key].point[1].toFixed(6)}, ${snaps[key].point[0].toFixed(6)}; gap ${snaps[key].distance_meters.toFixed(1)} m.${snaps[key].selection_reason?` Nearest was ${snaps[key].nearest_distance_meters.toFixed(1)} m. ${snaps[key].selection_reason}`:''}${snaps[key].destination_access?' Endpoint lies on mapped destination-only road; this does not grant private/customer access.':''}`).join(' '):'';
   if(!body.routes?.length){draw();status.textContent=`${body.openmaps?.message||'No driving route connects these endpoints.'} ${snapText} A/B are requested points; A′/B′ are road snaps. Dashed orange gaps are unverified off-road connectors, excluded from driving distance. Property entrances and off-road access are unknown.`;return;}
   const route=body.routes[0];geometry=route.polyline.geoJsonLinestring;draw();
   const distance=route.distanceMeters>=1000?`${(route.distanceMeters/1000).toFixed(2)} km`:`${route.distanceMeters} m`;
   status.textContent=`${distance} driving distance. ${snapText} A/B mark requested points; A′/B′ mark road snaps. Dashed orange gaps are unverified off-road connectors, excluded from driving distance. Property entrances and off-road access are unknown. No travel time is estimated.`;
   const map=getMap();if(map){const coords=[...geometry.coordinates,[origin.lng,origin.lat],[destination.lng,destination.lat]];const bounds=coords.reduce((b,p)=>b.extend(p),new (getMapLibre().LngLatBounds)(coords[0],coords[0]));map.fitBounds(bounds,{padding:65,maxZoom:16});}
  }catch(error){if(error.name!=='AbortError'&&current===generation)status.textContent=error.message;}
  finally{if(current===generation){busy=false;update();}}
 });
 window.addEventListener('focus',health);health();
 return {
  selection(point){selection=point;update();},
  mapReady:draw,
  mapClick(p){const action=mapAction.value;if(action==='address')return false;setEndpoint(action,{label:`Map point ${p.lat.toFixed(5)}, ${p.lng.toFixed(5)}`,lat:p.lat,lng:p.lng});return true;}
 };
}
