import java.net.*;
import java.io.*;
import java.nio.file.*;
import java.util.*;
import com.aliyun.credentials.Client;
import com.aliyun.credentials.models.Config;
import com.aliyun.credentials.models.CredentialModel;
public class JavaProbe {
 public static void main(String[] args) {
  URL.setURLStreamHandlerFactory(protocol -> protocol.equals("http") ? new URLStreamHandler() {
   protected URLConnection openConnection(URL u) throws IOException {
    final byte[] payload=Files.readAllBytes(Paths.get(System.getenv("SDK_FIXTURE_FILE")));
    return new HttpURLConnection(u) {
     public void connect() {} public void disconnect() {} public boolean usingProxy(){return false;}
     public int getResponseCode(){return 200;}
     public String getResponseMessage(){return "OK";}
     public InputStream getInputStream(){return new ByteArrayInputStream(payload);}
     public InputStream getErrorStream(){return null;}
     public Map<String,List<String>> getHeaderFields(){Map<String,List<String>> headers=new HashMap<>();headers.put("Content-Type",Collections.singletonList("application/json"));return headers;}
    };
   }
  } : null);
  boolean resolved=false, complete=false;
  String errorClass="";
  try {
   Client client=args[0].equals("explicit")?new Client(new Config().setType("credentials_uri").setCredentialsUri(args[1])):new Client();
   CredentialModel c=client.getCredential();
   resolved=true;
   complete="FAKE_ID".equals(c.getAccessKeyId())&&"FAKE_SECRET".equals(c.getAccessKeySecret())&&"FAKE_TOKEN".equals(c.getSecurityToken());
  } catch(Throwable e) {errorClass=e.getClass().getSimpleName();}
  System.out.println("{\"resolved\":"+resolved+",\"complete\":"+complete+",\"errorClass\":\""+errorClass+"\"}");
  System.exit(0);
 }
}
