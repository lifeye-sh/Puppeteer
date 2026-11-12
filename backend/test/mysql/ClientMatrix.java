import java.io.*;
import java.nio.file.*;
import java.sql.*;
import java.util.*;

/** Reads private connection properties; never prints credentials or result data. */
class ClientMatrix {
 public static void main(String[] args) throws Exception {
  Properties p=new Properties();try(var input=Files.newInputStream(Path.of(args[0]))){p.load(input);}
  p.setProperty("useServerPrepStmts","true");p.setProperty("emulateUnsupportedPstmts","false");p.setProperty("blobSendChunkSize","8192");p.setProperty("allowMultiQueries","false");
  try(Connection c=DriverManager.getConnection(p.getProperty("url"),p)){
   check(c.isValid(5),"PING");
   try(var s=c.createStatement();var r=s.executeQuery("SELECT @@transaction_read_only")){check(r.next()&&r.getInt(1)==1,"target readonly transaction");}
   try(var s=c.createStatement();var r=s.executeQuery("SELECT label FROM items WHERE id=1")){check(r.next()&&"R12 fixture".equals(r.getString(1)),"QUERY");}
   try(var s=c.prepareStatement("SELECT ?, ?")){
    s.setString(1,"redacted-parameter-验收");s.setNull(2,Types.VARCHAR);try(var r=s.executeQuery()){check(r.next()&&"redacted-parameter-验收".equals(r.getString(1))&&r.getString(2)==null,"typed/NULL bindings");}
    s.setString(1,"reused");s.setString(2,"prepared");try(var r=s.executeQuery()){check(r.next()&&"reused".equals(r.getString(1)),"prepared reuse");}
   }
   byte[] data=new byte[81920];Arrays.fill(data,(byte)0x5a);
   try(var s=c.prepareStatement("SELECT LENGTH(?)")){s.setBinaryStream(1,new ByteArrayInputStream(data),data.length);try(var r=s.executeQuery()){check(r.next()&&r.getInt(1)==data.length,"LONG_DATA");}}
   c.setAutoCommit(false);try(var s=c.createStatement();var r=s.executeQuery("SELECT count(*) FROM items")){check(r.next(),"transaction query");}c.rollback();c.setAutoCommit(true);
   c.setCatalog("inventory");
   try(var r=c.getMetaData().getTables("inventory",null,"items",new String[]{"TABLE"})){check(r.next(),"table metadata");}
   try(var r=c.getMetaData().getColumns("inventory",null,"items","%")){check(r.next(),"column metadata");}
   try(var s=c.createStatement()){try{s.executeUpdate("DELETE FROM items");throw new Exception("write was allowed");}catch(SQLException expected){}}
   try(var s=c.createStatement()){try{s.execute("START TRANSACTION READ WRITE");throw new Exception("readonly override allowed");}catch(SQLException expected){}}
   try(var s=c.createStatement()){try{s.execute("SELECT 1; DELETE FROM items");throw new Exception("multiple statements allowed");}catch(SQLException expected){}}
   try(var s=c.createStatement();var r=s.executeQuery("SELECT 1")){check(r.next()&&r.getInt(1)==1,"query after policy error");}
   System.out.println("PASS: Connector/J "+c.getMetaData().getDriverVersion()+" TLS, query, prepared bindings, LONG_DATA, transactions, metadata and denied writes");
  }
  Properties desktop=new Properties();desktop.putAll(p);desktop.setProperty("emulateUnsupportedPstmts","true");
  try(Connection c=DriverManager.getConnection(desktop.getProperty("url"),desktop)){
   try(var s=c.prepareStatement("use inventory")){s.execute();}
   for(String sql:new String[]{"SHOW ENGINES","SHOW CHARSET","SHOW COLLATION","SHOW PLUGINS","SHOW TABLE STATUS FROM inventory LIKE 'items'","SELECT @@character_set_server, @@collation_server","SELECT WORD FROM INFORMATION_SCHEMA.KEYWORDS WHERE RESERVED = 1 ORDER BY WORD"}){
    try(var s=c.prepareStatement(sql);var r=s.executeQuery()){check(r.next(),"DBeaver metadata: "+sql);}
   }
   try(var s=c.createStatement()){s.setMaxRows(200);try(var r=s.executeQuery("SELECT id,label FROM items ORDER BY id")){int rows=0;while(r.next())rows++;check(rows==2,"query after desktop initialization");}}
   System.out.println("PASS: DBeaver initialization metadata and subsequent query");
  }
  Properties wrong=new Properties();wrong.putAll(p);wrong.setProperty("password","incorrect-personal-password");deny(wrong,"incorrect password");
  Properties unsupported=new Properties();unsupported.putAll(p);unsupported.setProperty("defaultAuthenticationPlugin","com.mysql.cj.protocol.a.authentication.MysqlNativePasswordPlugin");unsupported.setProperty("disabledAuthenticationPlugins","com.mysql.cj.protocol.a.authentication.CachingSha2PasswordPlugin");deny(unsupported,"disabled required authentication plugin");
  System.out.println("PASS: invalid password and unsupported authentication rejected");
 }
 static void deny(Properties p,String label)throws Exception{try(Connection c=DriverManager.getConnection(p.getProperty("url"),p)){throw new Exception("Accepted "+label);}catch(SQLException expected){}}
 static void check(boolean ok,String step)throws Exception{if(!ok)throw new Exception("Failed: "+step);}
}
