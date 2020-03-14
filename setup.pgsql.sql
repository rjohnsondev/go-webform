-- Example setup file for Postgresql

DROP TABLE IF EXISTS test_form;
DROP TABLE IF EXISTS test_form_colours;
DROP TABLE IF EXISTS test_form_labels;
DROP TABLE IF EXISTS forms;

CREATE TABLE test_form_labels
(
    column_name        TEXT    NOT NULL PRIMARY KEY,
    label              TEXT    NOT NULL,
    description        TEXT    NOT NULL,
    placeholder        TEXT    NOT NULL,
    section_heading    TEXT    NOT NULL,
    options            TEXT    NOT NULL,
    options_as_radio   BOOLEAN NOT NULL,
    -- Regex only works for some types
    regex              TEXT    NOT NULL,
    linebreak_after    BOOLEAN NOT NULL,
    include_in_summary BOOLEAN NOT NULL
);

INSERT INTO test_form_labels
VALUES ('name', 'Customer Name', '', '', '', '', false, '', false, true),
       ('description', 'Description', 'Some extra *details* about __the customer__', '', '', '', true, '',
        true, false),
       ('age', 'Age of the customer', '', '', 'Customer Details', '', false, '', false, true),
       ('colour', 'Fav colour', '', '', '', 'Red,Green,Blue', true, '', false, true);

CREATE TABLE test_form
(
    id                SERIAL      NOT NULL PRIMARY KEY,
    created_ts        TIMESTAMPTZ NOT NULL,
    updated_ts        TIMESTAMPTZ NOT NULL,
    created_user      TEXT        NOT NULL,
    -- below is flexible -----
    name              VARCHAR     NOT NULL,
    description       TEXT,
    age               INT         NOT NULL,
    height            INT,
    sales_value       MONEY,
    fixed             DECIMAL,
    fraction_complete FLOAT,
    colour            VARCHAR     NOT NULL,
    -- Bools can't be not null
    is_active         BOOLEAN     NOT NULL,
    pickup_scheduled  timestamptz NOT NULL,
    dob               date        NOT NULL
);


CREATE TABLE forms
(
    form_id         SERIAL  NOT NULL PRIMARY KEY,
    name            TEXT    NOT NULL,
    description     TEXT    NOT NULL,
    path            TEXT    NOT NULL UNIQUE,
    table_name      TEXT    NOT NULL,
    admins          TEXT    NOT NULL,
    allow_anonymous BOOLEAN NOT NULL
);

INSERT INTO forms (name, description, path, table_name, admins, allow_anonymous)
VALUES ('Test Form', 'This is a test form', 'test_form', 'test_form', '', false);

