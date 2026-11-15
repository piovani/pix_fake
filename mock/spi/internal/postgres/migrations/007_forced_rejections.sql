-- An account can now make the simulated participant reject every payment with one reason ("REJECT:DS04"...).
ALTER TABLE accounts DROP CONSTRAINT accounts_scenario_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_scenario_check
    CHECK (scenario ~ '^(NORMAL|NO_ANSWER|REJECT:[A-Z0-9]{4})$');
