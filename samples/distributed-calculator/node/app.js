const express = require('express');
const bodyParser = require('body-parser');
const app = express();
app.use(bodyParser.json());
const cors = require('cors');
const port = 4000;

app.use(cors());

app.post('/divide', (req, res) => {
  let args = req.body;
  const [operandOne, operandTwo] = [Number(args['operandOne']), Number(args['operandTwo'])];
  
  console.log(`Dividing ${operandOne} by ${operandTwo}`);
  
  let result = operandOne / operandTwo;
  res.send(result.toString());
});

app.listen(port, () => console.log(`Listening on port ${port}!`));