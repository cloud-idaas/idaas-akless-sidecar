'use strict';
const [vendor, mode, target] = process.argv.slice(2);
(async () => {
 const result={resolved:false,complete:false};
 try {
  let values;
  if(vendor==='alibaba'){
   const payload=require('fs').readFileSync(process.env.SDK_FIXTURE_FILE);
   const http=require('@alicloud/credentials/dist/src/providers/http');
   http.doRequest=async()=>({statusCode:200,body:payload});
   const lib=require('@alicloud/credentials');
   const c=mode==='explicit'?new lib.default(new lib.Config({type:'credentials_uri',credentialsURI:target})):new lib.default();
   const cred=await c.getCredential();
   values=[cred.accessKeyId,cred.accessKeySecret,cred.securityToken];
  } else {
   const payload=require('fs').readFileSync(process.env.SDK_FIXTURE_FILE);
   require('@smithy/node-http-handler').NodeHttpHandler.create=()=>({handle:async()=>({response:{statusCode:200,headers:{},body:require('stream').Readable.from([payload])}}),destroy(){}});
   const lib=require('@aws-sdk/credential-providers');
   const p=lib.fromHttp({awsContainerCredentialsFullUri:target});
   const cred=await p();
   values=[cred.accessKeyId,cred.secretAccessKey,cred.sessionToken];
  }
  result.resolved=true;
  result.complete=JSON.stringify(values)===JSON.stringify(['FAKE_ID','FAKE_SECRET','FAKE_TOKEN']);
 } catch(e) {result.errorClass=e.constructor.name;}
 console.log(JSON.stringify(result));
 process.exit(0);
})().catch(()=>process.exit(1));
