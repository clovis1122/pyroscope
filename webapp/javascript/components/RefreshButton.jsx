import React from 'react';
import { useDispatch } from 'react-redux';

import { FontAwesomeIcon } from '@fortawesome/react-fontawesome';
import { faSyncAlt } from '@fortawesome/free-solid-svg-icons/faSyncAlt';
import { refresh } from '../redux/actions';

function RefreshButton() {
  const dispatch = useDispatch();
  return (
    <div>
      <button
        data-testid="refresh-btn"
        type="button"
        className="btn refresh-btn"
        onClick={() => dispatch(refresh())}
      >
        <FontAwesomeIcon icon={faSyncAlt} />
      </button>
    </div>
  );
}

export default RefreshButton;
