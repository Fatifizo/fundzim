CREATE DATABASE fundzim_perf OWNER fundzim_migrator;
REVOKE ALL ON DATABASE fundzim_perf FROM PUBLIC;
GRANT CONNECT ON DATABASE fundzim_perf TO fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly;
\c fundzim_perf
ALTER SCHEMA public OWNER TO fundzim_migrator;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
ALTER DATABASE fundzim_perf SET timezone TO 'UTC';
