SQL Forms
=========

This is a relatively simple dynamic web form generator for saving data into a SQL
backend.

## Compilation

This project uses go modules. Remember the classic https://engineering.kablamo.com.au/posts/2018/just-tell-me-how-to-use-go-modules

You'll need a `GOPATH` in the environment (just an empty directory will do):
`export GOPATH=/home/richard/Documents/gopath`

To compile for windows: `GOOS=windows go build`

## Setup

### Database

There are two example sql files showing how to setup the database for a simple form,
on for PostgreSQL, the other for SQLServer.

#### Creating a form

1. Create a new table with the desired fields:

    * The first 3 columns must be `id`, `created_ts`, `created_user` as per the
        template. These are inserted automatically by the system.
    * Columns of type `VARCHAR` are represented by single line text fields
    * `TEXT` are text areas
    * `INT` are text fields restricted to numbers
    * `BOOLEAN`/`BIT` are checkboxes
    * `TIMESTAMPTZ`/`DATETIMEOFFSET` are date / time dropdowns
    * Fields marked as `NOT NULL` will be shown as required in the form. Any empty
        strings entered into `NULL` form fields will be converted to `NULL`.

2. Create a metadata table for the form, which must be the name of the data table
with the suffix _labels.

    See the `setup.pgsql.sql` / `setup.mssql.sql` files for an example.
    This table is for the `test_form` data table:

    ```sql
    CREATE TABLE test_form_labels
    (
        column_name      VARCHAR(254)  NOT NULL PRIMARY KEY,
        label            VARCHAR(254)  NOT NULL,
        description      TEXT          NOT NULL,
        placeholder      VARCHAR(254)  NOT NULL,
        section_heading  VARCHAR(254)  NOT NULL,
        options          VARCHAR(1024) NOT NULL,
        options_as_radio BIT           NOT NULL,
        linebreak_after  BIT           NOT NULL
    );
    ```

3. Insert entries into the `_labels` metadata table as desired:

    * `column_name` must correspond with the column name in the form
    * `label` is the label before the input field
    * `description` is displayed after the field. This field can be expanded with 
        markdown for HTML formatting
    * `placeholder` provides a placeholder text for the field (only really applicable
        for input fields)
    * `section_heading` adds a heading before this field
    * `options` is a comma-separated list of options to provide the user. This is 
        a select field by default.
    * `options_as_radio` presents the options as radio buttons rather than a
        select (drop-down) field.
    * `linebreak_after` is slightly misnamed. It actually adds a horizontal rule
        after the field.
        
    Note that if a field exists, but does not have an entry in the `_labels` table,
    it will still be shown with sensible defaults.
    
4. Add entry into the `forms` table. This will make the form accessible

    ```sql
    INSERT INTO forms (name, description, path, table_name)
    VALUES ('Test Form', 'This is a test form', 'test_form', 'test_form');
    ```
   
   The form should be accessible at: https://servername/path

### Configuration

The config file is `config.toml`. It can be changed to have windows line endings
if it all appears on a single line.

#### server

The server will listen on HTTPS if a certificate file is provided. A simple file
can be generated with `openssl` like so:
```
openssl genrsa -out https-server.key 2048
openssl ecparam -genkey -name secp384r1 -out https-server.key
openssl req -new -x509 -sha256 -key https-server.key -out https-server.crt -days 3650
```
These files are specified as `certificate` and `key`.

`listen` indicates the port for the web server to bind to. Note that port 80 and 443
are privileged, and therefore the system would need to run as Administrator/root.

`staticDir` should be the path to the few CSS / JS files required from the `static`
directory.

`template` should be the path to the index.template.html file.

#### database

Should be pretty self-explanatory.

#### auth

The system will attempt to use SPNEGO browser authentication for single-sign-on
goodness when running in a Windows Active Directory domain.

The `keytab` file enables this, and should point to the generated keytab file
for the service. If it is empty, all entries will be recorded under `anonymous`.

`cookieName` and `sessionKey` should be customised as desired.

## Server Setup

### User Authentication

To enable single-sign-on, there's a bit of server configuration required.

For some background reading and a how-to guide before getting into it (strongly
recommended) see:
https://social.technet.microsoft.com/wiki/contents/articles/36470.active-directory-using-kerberos-keytabs-to-integrate-non-windows-systems.aspx?wa=wsignin1.0

1. In the DNS settings, add a `A` record for the IP where the service will be hosted

    **NOTE:** A `CNAME` (or alias) is _not_ adequate, and will result in auth errors.

    For this example we'll use: `forms.tsa.local`

2. Create an AD user for the service.

   This user will be used as the service account for the processing of SSO requests.
   It is separate from the database authentication, and is not the same as the
   user that the service runs as.
   
   The username we'll use in this example is `webforms@tsa.local` and
   `TSA\webforms`
   
   * Give an initial password, mark it as non-expiring, user can't change.

3. Run the magic command, customising as required:

   `ktpass -out webforms.keytab -mapUser webforms@TSA.LOCAL +rndPass -mapOp set +DumpSalt -crypto AES256-SHA1 -ptype KRB5_NT_PRINCIPAL -princ HTTP/webforms.tsa.local@TSA`
   
   Note the domain is uppercase in the `-mapUser` and the prefix of `HTTP/` and
   addition of both the new and classic domain names for the `-princ`
   
   The generated webforms.keytab file is what we need to provide to the service.
   
   This would have modified the user appropriately to be a service account.
   
4. As per the website's instructions, ensure the User's Account tab has `this 
    account supports Kerberos AES256 bit encryption` checked.

#### Client browser setup

In order for the authentication handshake to work, the site must be added to
the list of intranet websites under Internet Options -> Security -> Local
Intranet -> Sites (or the applicable registry entries). Remember the `https://`
when adding.

### Database Connectivity

As supported by the driver: https://github.com/denisenkom/go-mssqldb

### Service Installation

Note that the user the service runs as is different to the service account for
processing SSO requests as created above.

The service is not a fully-contained Windows or Linux service / daemon. It is
recommended that a service shim be used such as http://nssm.cc/ to run the
service.

### Required files for the service

* `sql-form.exe` or whatever the binary is called
* `config.toml` should be in the running directory, points to all of the below
* `webforms.keytab` or as created above
* `https-server.crt/key` as generated for TLS
* `static` directory
* `index.template.html`

