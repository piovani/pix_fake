-- Read streams no longer keep state: the PI-Pull-Next token is the position in the queue itself.
DROP TABLE IF EXISTS streams;
