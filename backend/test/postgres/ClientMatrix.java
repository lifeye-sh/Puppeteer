import java.sql.*;
import java.util.*;
import java.util.concurrent.*;

/** Runs only against the disposable Go integration fixture. Never prints secrets. */
class ClientMatrix {
 static void check(boolean ok,String label) throws Exception { if(!ok)throw new Exception(label); }
 static void step(String name){System.out.println("STEP "+name);}
 static int count(ResultSet r)throws Exception{try(r){int n=0;while(r.next())n++;return n;}}
 static void denied(Connection c,String sql)throws Exception{
  try(var s=c.createStatement()){try{s.execute(sql);throw new Exception("Unsafe command accepted");}catch(SQLException e){check("42501".equals(e.getSQLState()),"unexpected denial "+e.getSQLState());}}
 }
 public static void main(String[] args)throws Exception{
  Properties p=new Properties();p.setProperty("user","pg-reader@pgnative");p.setProperty("password",System.getenv("NT_JDBC_PASSWORD"));
  p.setProperty("sslmode","verify-full");p.setProperty("sslrootcert",System.getenv("NT_JDBC_CA"));p.setProperty("requireAuth","scram-sha-256");
  p.setProperty("prepareThreshold","1");p.setProperty("connectTimeout","5");p.setProperty("socketTimeout","20");
  String url="jdbc:postgresql://localhost:"+System.getenv("NT_JDBC_PORT")+"/inventory";
  step("connect");
  try(Connection c=DriverManager.getConnection(url,p)){
   check(c.isValid(5),"isValid");
   step("readonly and timezone");
   try(var s=c.createStatement();var r=s.executeQuery("SHOW transaction_read_only")){check(r.next()&&"on".equals(r.getString(1)),"target readonly");}
   try(var s=c.createStatement();var r=s.executeQuery("SHOW TimeZone")){check(r.next()&&"Asia/Shanghai".equals(r.getString(1)),"startup TimeZone ignored");}
   step("prepared bindings and reuse");
   try(var s=c.prepareStatement("SELECT ?::int4,?::text,?::bytea,?::text")){
    for(int i=0;i<3;i++){s.setInt(1,i);s.setString(2,"jdbc-秘密参数");s.setBytes(3,new byte[]{0,1,(byte)255});s.setNull(4,Types.VARCHAR);try(var r=s.executeQuery()){check(r.next()&&r.getInt(1)==i&&"jdbc-秘密参数".equals(r.getString(2))&&Arrays.equals(r.getBytes(3),new byte[]{0,1,(byte)255})&&r.getString(4)==null,"typed values");}}
   }
   step("metadata tables");check(count(c.getMetaData().getTables(null,"public","items",new String[]{"TABLE"}))==1,"tables");
   step("metadata columns");check(count(c.getMetaData().getColumns(null,"public","items","%"))==2,"columns");
   step("metadata keys");check(count(c.getMetaData().getPrimaryKeys(null,"public","items"))==1,"primary keys");
   step("metadata indexes");check(count(c.getMetaData().getIndexInfo(null,"public","items",false,false))>=1,"indexes");
   step("metadata schemas");check(count(c.getMetaData().getSchemas())>=1,"schemas");
   step("metadata types");check(count(c.getMetaData().getTypeInfo())>=1,"types");
   step("metadata procedures");count(c.getMetaData().getProcedures(null,"public","%"));
   step("metadata foreign keys");count(c.getMetaData().getImportedKeys(null,"public","items"));count(c.getMetaData().getExportedKeys(null,"public","items"));
   step("metadata privileges");count(c.getMetaData().getTablePrivileges(null,"public","items"));count(c.getMetaData().getColumnPrivileges(null,"public","items","%"));
   step("transaction isolation");c.setTransactionIsolation(Connection.TRANSACTION_REPEATABLE_READ);check(c.getTransactionIsolation()==Connection.TRANSACTION_REPEATABLE_READ,"isolation readback");
   step("readonly transaction, savepoints, fetch size");c.setReadOnly(true);c.setAutoCommit(false);
   try(var s=c.createStatement()){s.setFetchSize(1);try(var r=s.executeQuery("SELECT id,label FROM items ORDER BY id")){check(r.next()&&r.getInt(1)==1&&r.next()&&r.getInt(1)==2&&!r.next(),"cursor fetch");}}
   Savepoint sp=c.setSavepoint();c.rollback(sp);c.releaseSavepoint(sp);c.commit();c.setAutoCommit(true);
   step("max rows");try(var s=c.createStatement()){s.setMaxRows(1);check(count(s.executeQuery("SELECT id FROM items ORDER BY id"))==1,"max rows");}
   step("cancel and recovery");try(var s=c.createStatement()){
    var executor=Executors.newSingleThreadExecutor();try{Future<Boolean> f=executor.submit(()->{try{s.execute("SELECT pg_sleep(8)");return false;}catch(SQLException e){return "57014".equals(e.getSQLState());}});Thread.sleep(300);s.cancel();check(f.get(5,TimeUnit.SECONDS),"cancel state");}finally{executor.shutdownNow();}
    check(count(s.executeQuery("SELECT 1"))==1,"cancel recovery");
   }
   step("denied writes and recovery");denied(c,"DELETE FROM items");denied(c,"BEGIN READ WRITE");denied(c,"SET default_transaction_read_only=off");
   try(var s=c.createStatement()){check(count(s.executeQuery("SELECT 1"))==1,"policy recovery");}
   System.out.println("PASS pgJDBC "+c.getMetaData().getDriverVersion()+" verified TLS, SCRAM, metadata, parameters, cursor, transaction, cancellation and denials");
  }
  Properties wrong=new Properties();wrong.putAll(p);wrong.setProperty("password","wrong");
  try(Connection c=DriverManager.getConnection(url,wrong)){throw new Exception("wrong password accepted");}catch(SQLException expected){check("28P01".equals(expected.getSQLState()),"password denial");}
  Properties always=new Properties();always.putAll(p);always.setProperty("readOnly","true");always.setProperty("readOnlyMode","always");
  try(Connection c=DriverManager.getConnection(url,always);var s=c.createStatement()){check(count(s.executeQuery("SELECT 1"))==1,"readonly mode always");}
  System.out.println("PASS invalid password and readOnlyMode=always");
 }
}
