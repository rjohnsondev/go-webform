-- Example setup file for SQL SERVER

DROP TABLE IF EXISTS test_form;
DROP TABLE IF EXISTS test_form_colours;
DROP TABLE IF EXISTS test_form_labels;
DROP TABLE IF EXISTS forms;

CREATE TABLE test_form_labels
(
    column_name        VARCHAR(254)  NOT NULL PRIMARY KEY,
    label              VARCHAR(254)  NOT NULL,
    description        TEXT          NOT NULL,
    placeholder        VARCHAR(254)  NOT NULL,
    section_heading    VARCHAR(254)  NOT NULL,
    options            VARCHAR(1024) NOT NULL,
    options_as_radio   BIT           NOT NULL,
    -- Regex only works for some types
    regex              VARCHAR(1024) NOT NULL,
    linebreak_after    BIT           NOT NULL,
    include_in_summary BIT           NOT NULL
);

INSERT INTO test_form_labels
VALUES ('name', 'Customer Name', '', '', '', '', 0, '', 0, 1),
       ('description', 'Description', 'Some extra *details* about __the customer__', '', '', '', 1, '',
        1, 0),
       ('age', 'Age of the customer', '', '', 'Customer Details', '', 0, '', 0, 1),
       ('colour', 'Fav colour', '', '', '', 'Red,Green,Blue', 1, '', 0, 1);

CREATE TABLE test_form
(
    id                INT            NOT NULL IDENTITY PRIMARY KEY,
    created_ts        DATETIMEOFFSET NOT NULL,
    updated_ts        DATETIMEOFFSET NOT NULL,
    created_user      VARCHAR(254)   NOT NULL,
    -- below is flexible -----
    name              VARCHAR(1024)  NOT NULL,
    description       TEXT,
    age               INT            NOT NULL,
    height            INT,
    sales_value       MONEY,
    fixed             DECIMAL,
    fraction_complete FLOAT,
    colour            VARCHAR(1024)  NOT NULL,
    -- Bools can't be not null
    is_active         BIT            NOT NULL,
    pickup_scheduled  DATETIMEOFFSET NOT NULL,
    dob               DATE           NOT NULL
);


CREATE TABLE forms
(
    form_id         INT           NOT NULL IDENTITY PRIMARY KEY,
    name            VARCHAR(254)  NOT NULL,
    description     TEXT          NOT NULL,
    path            VARCHAR(254)  NOT NULL UNIQUE,
    table_name      VARCHAR(254)  NOT NULL,
    admins          VARCHAR(1024) NOT NULL,
    allow_anonymous BIT           NOT NULL
);

INSERT INTO forms (name, description, path, table_name, admins, allow_anonymous)
VALUES ('Test Form', 'This is a test form', 'test_form', 'test_form', '', 0);

