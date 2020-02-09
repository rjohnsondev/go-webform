-- Example setup file for SQL SERVER

DROP TABLE IF EXISTS test_form;
DROP TABLE IF EXISTS test_form_colours;
DROP TABLE IF EXISTS test_form_labels;
DROP TABLE IF EXISTS forms;

CREATE TABLE test_form_colours
(
    colour VARCHAR(254) NOT NULL PRIMARY KEY
);
INSERT INTO test_form_colours
VALUES ('Red'),
       ('Green'),
       ('Blue');

CREATE TABLE test_form_labels
(
    column_name     VARCHAR(254) NOT NULL PRIMARY KEY,
    label           VARCHAR(254) NOT NULL,
    description     TEXT NOT NULL,
    placeholder     VARCHAR(254) NOT NULL,
    section_heading VARCHAR(254) NOT NULL,
    linebreak_after BIT          NOT NULL
);

INSERT INTO test_form_labels
VALUES ('name', 'Customer Name', '', '', '', 0),
       ('description', 'Description', 'Some extra *details* about __the customer__', '', '', 1),
       ('age', 'Age of the customer', '', '', 'Customer Details', 0);

CREATE TABLE test_form
(
    id               INT            NOT NULL IDENTITY PRIMARY KEY,
    created_ts       DATETIMEOFFSET NOT NULL,
    created_user     VARCHAR(254)   NOT NULL,
    -- below is flexible -----
    name             VARCHAR(1024)   NOT NULL,
    description      TEXT,
    age              INT            NOT NULL,
    height           INT,
    colour           VARCHAR(254)   NOT NULL references test_form_colours (colour),
    is_active        BIT            NOT NULL,
    pickup_scheduled DATETIMEOFFSET NOT NULL
);


CREATE TABLE forms
(
    form_id     INT          NOT NULL IDENTITY PRIMARY KEY,
    name        VARCHAR(254) NOT NULL,
    description TEXT NOT NULL,
    path        VARCHAR(254) NOT NULL UNIQUE,
    table_name  VARCHAR(254) NOT NULL
);

INSERT INTO forms (name, description, path, table_name)
VALUES ('Test Form', 'This is a test form', 'test_form', 'test_form');

