'use strict';
function fillSampleRow(form, schema, primaryKeys) {
  const booleanValues = ['true', 'false'];
  const primary = schema.columns.find(column => column.name === schema.primary_key);
  const randomInteger = () => Math.floor(Math.random() * 1000) + 1;
  const randomText = () => `sample-${Math.floor(Math.random() * 36 ** 6).toString(36).padStart(6, '0')}`;
  let primaryValue;
  switch (primary.type) {
    case 'integer': {
      let value = randomInteger();
      while (primaryKeys.has(String(value))) value++;
      primaryValue = String(value);
      break;
    }
    case 'string': {
      const prefix = randomText();
      primaryValue = prefix;
      for (let suffix = 1; primaryKeys.has(primaryValue); suffix++) {
        primaryValue = `${prefix}-${suffix}`;
      }
      break;
    }
    case 'boolean': {
      const available = booleanValues.filter(value => !primaryKeys.has(value));
      if (available.length === 0) {
        throw new Error('主キーの true と false はどちらも使用済みです。');
      }
      primaryValue = available[Math.floor(Math.random() * available.length)];
      break;
    }
  }
  for (const column of schema.columns) {
    const field = form.elements.namedItem(column.name);
    if (column === primary) {
      field.value = primaryValue;
      continue;
    }
    switch (column.type) {
      case 'integer': field.value = String(randomInteger()); break;
      case 'string': field.value = randomText(); break;
      case 'boolean': field.value = booleanValues[Math.floor(Math.random() * booleanValues.length)]; break;
    }
  }
}
