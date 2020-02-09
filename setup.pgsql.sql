-- Example setup file for Postgresql

DROP TABLE IF EXISTS test_form;
DROP TABLE IF EXISTS test_form_colours;
DROP TABLE IF EXISTS test_form_labels;
DROP TABLE IF EXISTS forms;

CREATE TABLE test_form_colours
(
    colour VARCHAR NOT NULL PRIMARY KEY
);
INSERT INTO test_form_colours
VALUES ('Red'),
       ('Green'),
       ('Blue');

CREATE TABLE test_form_labels
(
    column_name     TEXT    NOT NULL PRIMARY KEY,
    label           TEXT    NOT NULL,
    description     TEXT    NOT NULL,
    placeholder     TEXT NOT NULL,
    section_heading TEXT NOT NULL,
    linebreak_after BOOLEAN NOT NULL
);

INSERT INTO test_form_labels
VALUES ('name', 'Customer Name', '', '', '', false),
       ('description', 'Description', 'Some extra *details* about __the customer__', '', '', true),
       ('age', 'Age of the customer', '', '', 'Customer Details', false);

CREATE TABLE test_form
(
    id               SERIAL      NOT NULL PRIMARY KEY,
    created_ts       TIMESTAMPTZ NOT NULL,
    created_user     TEXT        NOT NULL,
    -- below is flexible -----
    name             VARCHAR     NOT NULL,
    description      TEXT,
    age              INT         NOT NULL,
    height           INT,
    colour           VARCHAR     NOT NULL references test_form_colours (colour),
    is_active        BOOLEAN     NOT NULL,
    pickup_scheduled timestamptz NOT NULL
);


CREATE TABLE forms
(
    form_id     SERIAL NOT NULL PRIMARY KEY,
    name        TEXT   NOT NULL,
    description TEXT   NOT NULL,
    path        TEXT   NOT NULL UNIQUE,
    table_name  TEXT   NOT NULL
);

INSERT INTO forms (name, description, path, table_name)
VALUES ('Test Form', 'This is a test form', 'test_form', 'test_form');

