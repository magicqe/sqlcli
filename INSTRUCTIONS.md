# Instructions given in the chat

Every instruction from the development chat for sqlcli, in order and word for
word (typos kept). Passwords are replaced with `****`; they are in `test.env`.

## Starting point: `Calude.md` (now `Claude.md`)

```
Create go terminal application
like  https://github.com/kackerx/sqlmux/
with oracle database support(use godror)
use text editor with joe shortcuts
save aplication configuration to text file
and sql commands history to sqlite database
```

## Chat

1. ```
   read claude.md
   ```

2. ```
   use toml
   ```

3. Sent while the previous step was running:
   ```
   ORACLE_HOME=/usr/local/oracle
   ```

4. ```
   test against existing database net.ora.sk men/****
   ```

5. ```
   do it
   ```

6. ```
   password is ****
   ```

7. ```
   SQLCLI_TEST_CONNECT = net.ora.sk
   SQLCLI_TEST_USER = men
   SQLCLI_TEST_PASSWORD = ****
   ```

8. ```
   create manual
   ```

9. ```
   manual as web page
   ```

10. ```
    if select statemnet are on two lines F5 (run) not working on first line try:
    ```

11. Sent while the previous step was running:
    ```
    select * from netlist;
    select * from nsm;
    ```

12. ```
    add option to get all rows from select and read more data on scroll and get all rows on "end" key
    ```

13. ```
    add skorcut io show/hide connection panel and put connection name in top of sql panel window
    ```

14. ```
    if run plsql, put in result window dbms_output
    ```

15. ```
    add support for multiple sql panel and multiple connections in same time
    ```

16. ```
    add export to json/csv to sql result
    ```

17. ```
    ctrl k+x not working in result panel, add mouse click to change panel and close sql window
    ```

18. ```
    add cursor up/down home/end ctr+a,ctrl+e to show next/previous first/last record
    ```

19. ```
    add page up/down to move cursor in result record view by in config defined value
    ```

20. ```
    add query running time and status on the bottom of the sql panel
    ```

21. ```
    put all my insctructions on chat to file
    ```
