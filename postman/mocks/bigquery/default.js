const http = require('http');
const jobs = new Map();
const metadataReads=[];
let nextJob = 1;
const fields = [
 {name:'id', type:'INTEGER'}, {name:'value', type:'STRING'},
 {name:'payload', type:'BYTES'}, {name:'tags', type:'STRING', mode:'REPEATED'},
 {name:'amount',type:'NUMERIC'}
];
function response(res, status, data) { res.writeHead(status, {'Content-Type':'application/json'}); res.end(JSON.stringify(data)); }
function failure(res, code, reason, message) { response(res,code,{error:{code,message,errors:[{reason,message}]}}); }
http.createServer(async (req,res) => {
 try {
  const url = new URL(req.url,'http://localhost');
  const path = url.pathname;
  let body = req.body;
  if (!body && ['POST','PUT'].includes(req.method)) {
   let raw=''; for await (const chunk of req) raw+=chunk; body = raw ? JSON.parse(raw) : {};
  }
  if (typeof body === 'string') body=JSON.parse(body);
  if (path==='/__state') return response(res,200,{jobs:[...jobs.values()],metadataReads});
  if(path.endsWith('/tables/events/rowAccessPolicies')) return response(res,200,{rowAccessPolicies:[{rowAccessPolicyReference:{projectId:'test-project',datasetId:'test_dataset',tableId:'events',policyId:'regional'},filterPredicate:"region = 'US'"}]});
  if(path.endsWith('/tables/events:getIamPolicy') && req.method==='POST') {
   if(body.options?.requestedPolicyVersion!==3) return failure(res,400,'invalid','Policy version 3 required');
   return response(res,200,{version:3,bindings:[{role:'roles/bigquery.dataViewer',members:['group:analysts@example.org'],condition:{title:'limited',expression:'request.time < timestamp("2030-01-01T00:00:00Z")'}}]});
  }
  if (path.endsWith('/datasets') && req.method==='GET') {
   const datasets=url.searchParams.get('pageToken')==='next' ? ['restricted_dataset','empty_dataset'] : ['test_dataset'];
   return response(res,200,{kind:'bigquery#datasetList',datasets:datasets.map(datasetId=>({kind:'bigquery#dataset',datasetReference:{projectId:'test-project',datasetId}})),...(datasets[0]==='test_dataset'?{nextPageToken:'next'}:{})});
  }
  if (path.endsWith('/datasets/restricted_dataset/tables')) return failure(res,403,'accessDenied','Dataset access denied');
  if (path.endsWith('/datasets/empty_dataset/tables')) return response(res,200,{kind:'bigquery#tableList',tables:[]});
  if (path.endsWith('/datasets/test_dataset') && req.method==='GET') return response(res,200,{kind:'bigquery#dataset',datasetReference:{projectId:'test-project',datasetId:'test_dataset'},location:'US'});
  const tableMatch=path.match(/\/datasets\/([^/]+)\/tables\/([^/]+)$/);
  if(tableMatch && req.method==='GET') {
   const tableId=decodeURIComponent(tableMatch[2]); metadataReads.push(tableId);
   if(tableId==='denied_metadata') return failure(res,403,'accessDenied','Metadata access denied');
   if(tableId==='missing_metadata') return failure(res,404,'notFound','Table no longer exists');
   if(tableId==='slow_metadata') await new Promise(resolve=>setTimeout(resolve,1000));
   const table={kind:'bigquery#table',tableReference:{projectId:'test-project',datasetId:tableMatch[1],tableId},type:'TABLE',numRows:'42',description:'Nested event data',schema:{fields:[
    {name:'id',type:'INTEGER',mode:'REQUIRED'},
    {name:'occurred_at',type:'TIMESTAMP'},
    {name:'account_id',type:'INTEGER'},
    {name:'profile',type:'RECORD',fields:[{name:'address',type:'RECORD',fields:[{name:'city',type:'STRING'}]}]},
    {name:'events',type:'RECORD',mode:'REPEATED',fields:[{name:'code',type:'STRING',description:'Event code'}]}
   ]},timePartitioning:{type:'DAY',field:'occurred_at',expirationMs:'2592000000'},requirePartitionFilter:true,clustering:{fields:['id','account_id']},tableConstraints:{primaryKey:{columns:['id']},foreignKeys:[{name:'account_fk',referencedTable:{projectId:'test-project',datasetId:'test_dataset',tableId:'accounts'},columnReferences:[{referencingColumn:'account_id',referencedColumn:'id'}]}]}};
   table.labels={team:'analytics'};table.expirationTime='1893456000000';table.encryptionConfiguration={kmsKeyName:'projects/test/locations/us/keyRings/test/cryptoKeys/key'};
   if(tableId==='ingestion') table.timePartitioning={type:'DAY'};
   if(tableId==='range_events') { delete table.timePartitioning;table.rangePartitioning={field:'id',range:{start:'0',end:'1000',interval:'100'}}; }
   if(tableId==='orders') { table.type='VIEW';table.view={query:'SELECT id FROM events',useLegacySql:false};delete table.timePartitioning;delete table.clustering; }
   return response(res,200,table);
  }
  if (path.endsWith('/tables') && req.method==='GET') {
   const tables = url.searchParams.get('pageToken')==='next' ? ['orders'] : ['events'];
   return response(res,200,{kind:'bigquery#tableList',tables:tables.map(tableId=>({kind:'bigquery#table',type:tableId==='orders'?'VIEW':'TABLE',...(tableId==='events'?{timePartitioning:{type:'DAY',field:'occurred_at'},clustering:{fields:['id','account_id']}}:{}),tableReference:{projectId:'test-project',datasetId:'test_dataset',tableId}})),...(tables[0]==='events'?{nextPageToken:'next'}:{})});
  }
  if (path.endsWith('/jobs') && req.method==='POST') {
   const q=body.configuration?.query;
   if (!q || q.useLegacySql!==false || !q.maximumBytesBilled || (q.defaultDataset && q.defaultDataset.datasetId!=='test_dataset')) return failure(res,400,'invalid','Missing standard SQL, billing cap, or invalid default dataset');
   if (q.query.endsWith('-- project-only') && q.defaultDataset) return failure(res,400,'invalid','Project-only query must omit defaultDataset');
   if (q.query==='invalid') return failure(res,400,'invalidQuery','Syntax error in SQL');
	if (q.query.includes("'denied_metadata'")) return failure(res,403,'accessDenied','Metadata access denied');
   const jobId=body.jobReference?.jobId || `job-${nextJob++}`;
   const job={jobReference:{projectId:'test-project',location:'US',jobId},configuration:body.configuration,status:{state:'DONE'},cancelled:false};
	if(q.query==='write') job.statistics={creationTime:String(Date.now()),startTime:String(Date.now()),endTime:String(Date.now()),query:{numDmlAffectedRows:'3'}};
   jobs.set(jobId,job);
   return response(res,200,job);
  }
  const match = path.match(/\/(jobs|queries)\/([^/]+)(\/cancel)?$/);
  if (match) {
   const job=jobs.get(decodeURIComponent(match[2]));
   if (!job) return failure(res,404,'notFound','Job not found');
   if (match[3]) { job.cancelled=true; return response(res,200,{kind:'bigquery#jobCancelResponse',job}); }
   if (match[1]==='jobs') {
    if (job.configuration.query.query==='slow') return response(res,200,{...job,status:{state:'RUNNING'}});
    return response(res,200,job);
   }
   const query=job.configuration.query.query;
   if (query==='slow') return response(res,200,{jobComplete:false,jobReference:job.jobReference});
   if (query==='denied') return failure(res,403,'accessDenied','Permission denied for query results');
	let metadata;
 if(query.includes('TO_JSON_STRING(m)')) {
  let values=[];
  if(query.includes('INFORMATION_SCHEMA.PARTITIONS')) values=[{partition_id:'20261105',storage_tier:'ACTIVE',total_rows:'42',total_logical_bytes:'1024'}];
  if(query.includes('INFORMATION_SCHEMA.SEARCH_INDEXES')) values=[{index_name:'events_search',index_status:'ACTIVE',ddl:'CREATE SEARCH INDEX events_search ON events(ALL COLUMNS)'}];
  if(query.includes('INFORMATION_SCHEMA.VECTOR_INDEXES')) values=[{index_name:'events_vector',index_status:'ACTIVE',index_type:'IVF',coverage_percentage:100}];
  metadata={fields:[{name:'metadata',type:'STRING'}],values:values.map(v=>[JSON.stringify(v)])};
 }
	if(query.includes('INFORMATION_SCHEMA.COLUMNS')) metadata={fields:[{name:'column_name',type:'STRING'},{name:'data_type',type:'STRING'},{name:'is_nullable',type:'STRING'}], values:[['id','INT64','NO'],['name','STRING','YES']]};
	if(query.includes('INFORMATION_SCHEMA.KEY_COLUMN_USAGE')) metadata={fields:[{name:'column_name',type:'STRING'},{name:'constraint_type',type:'STRING'}], values:[['id','PRIMARY KEY']]};
	if(query.includes('INFORMATION_SCHEMA.TABLES`')) metadata={fields:[{name:'ddl',type:'STRING'}],values:[['CREATE TABLE `test-project.test_dataset.events` (id INT64, name STRING);']]};
	if(query.includes('INFORMATION_SCHEMA.TABLE_STORAGE')) metadata={fields:[{name:'total_rows',type:'INTEGER'}],values:[['42']]};
	if(metadata) return response(res,200,{jobComplete:true,jobReference:job.jobReference,schema:{fields:metadata.fields},totalRows:String(metadata.values.length),rows:requestedRows(metadata.values,url)});
   const count=query==='large'?1002:1;
   const offset=Number(url.searchParams.get('pageToken')||0);
   const requested=Number(url.searchParams.get('maxResults')??100);
   const size=Math.min(requested,600);
   const end=Math.min(offset+size,count);
   const rows=Array.from({length:Math.max(0,end-offset)},(_,i)=>({f:[{v:String(offset+i+1)},{v:null},{v:'AQI='},{v:[{v:'one'},{v:'two'}]},{v:'123.456789123'}]}));
   return response(res,200,{kind:'bigquery#getQueryResultsResponse',jobComplete:true,jobReference:job.jobReference,schema:{fields},totalRows:String(count),rows,...(end<count && size>0?{pageToken:String(end)}:{})});
  }
  failure(res,501,'notImplemented','Endpoint outside the dibiex simulation contract');
 } catch(e) { failure(res,500,'internalError',e.message); }
}).listen(process.env.PORT || 4500);

function requestedRows(values,url) { return url.searchParams.get('maxResults')==='0'?[]:values.map(row=>({f:row.map(v=>({v}))})); }


